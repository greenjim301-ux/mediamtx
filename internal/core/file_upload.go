package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/fileurl"
	ssfile "github.com/bluenviron/mediamtx/internal/staticsources/file"
	"github.com/google/uuid"
)

// APIManagementUploadSettings resolves relative storage paths against the config file.
func (p *Core) APIManagementUploadSettings() (string, uint64, error) {
	c := p.apiConfigSnapshot()
	dir := c.APIFileUploadDirectory
	if !filepath.IsAbs(dir) {
		base := "."
		if p.confPath != "" {
			base = filepath.Dir(p.confPath)
		}
		dir = filepath.Join(base, dir)
	}
	abs, err := filepath.Abs(dir)
	return abs, uint64(c.APIFileUploadMaxSize), err
}

type cancelReader struct {
	ctx context.Context
	r   io.Reader
}

func (r cancelReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

// APIManagementUpload streams and validates outside the core loop, then serializes
// the durable source creation. All unsuccessful uploads are removed.
func (p *Core) APIManagementUpload(ctx context.Context, name, original string, r io.Reader) error {
	uploadCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopCore := context.AfterFunc(p.ctx, cancel)
	defer stopCore()
	readerStopped := make(chan struct{})
	stopReader := context.AfterFunc(uploadCtx, func() {
		defer close(readerStopped)
		if closer, ok := r.(io.Closer); ok {
			closer.Close()
		}
	})
	defer func() {
		if !stopReader() {
			<-readerStopped
		}
	}()
	ctx = uploadCtx
	if p.confPath == "" {
		return fmt.Errorf("upload requires a writable configuration file")
	}
	if err := conf.IsValidPathName(name); err != nil {
		return err
	}
	if name == "all" || name == "all_others" || strings.HasPrefix(name, "~") {
		return fmt.Errorf("a named source is required")
	}
	if _, ok := p.apiConfigSnapshot().OptionalPaths[name]; ok {
		return fmt.Errorf("path already exists")
	}
	original = filepath.Base(strings.ReplaceAll(original, "\\", "/"))
	ext := strings.ToLower(filepath.Ext(original))
	if ext != ".mp4" && ext != ".mkv" {
		return fmt.Errorf("only MP4 and MKV files can be uploaded")
	}
	dir, maxSize, err := p.APIManagementUploadSettings()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	filename := "mediamtx-upload-" + uuid.NewString() + ext
	f, err := root.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		f.Close()
		if !committed {
			root.Remove(filename)
		}
	}()
	n, err := io.Copy(f, io.LimitReader(cancelReader{ctx, r}, int64(maxSize)+1))
	if err != nil {
		return err
	}
	if uint64(n) > maxSize {
		return defs.ErrFileUploadTooLarge
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	path := filepath.Join(dir, filename)
	if err = ssfile.Validate(ctx, path); err != nil {
		return fmt.Errorf("unsupported or invalid video: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	change := conf.ManagementChange{Action: "upload", Name: name, UploadDirectory: dir, UploadFileName: filename, UploadOriginalName: original}
	// Once submitted, commit to a definitive result, even if the HTTP client disconnects.
	res := make(chan error)
	select {
	case p.chManagement <- managementReq{change, res}:
		err = <-res
	case <-ctx.Done():
		return ctx.Err()
	case <-p.ctx.Done():
		return fmt.Errorf("terminated")
	}
	if err != nil {
		return err
	}
	committed = true
	return nil
}

func uploadedPath(change conf.ManagementChange) (*conf.OptionalPath, error) {
	source := (&url.URL{Scheme: "file", Path: filepath.Join(change.UploadDirectory, change.UploadFileName)}).String()
	b, err := json.Marshal(map[string]any{"source": source, "fileUploadDirectory": change.UploadDirectory, "fileUploadOriginalName": change.UploadOriginalName})
	if err != nil {
		return nil, err
	}
	var optional conf.OptionalPath
	err = json.Unmarshal(b, &optional)
	return &optional, err
}

type uploadDeletion struct {
	root            *os.Root
	name, tombstone string
}

// Stage only an owned, regular uploaded file. Root-relative operations do not
// follow file symlinks and cannot escape the directory recorded with the upload.
func (p *Core) stageUploadDeletion(name string, current *conf.Path) (*uploadDeletion, error) {
	if current.FileUploadDirectory == "" {
		return nil, nil
	}
	path, err := fileurl.Path(current.Source)
	if err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(current.FileUploadDirectory)
	if err != nil {
		return nil, err
	}
	file := filepath.Base(path)
	if filepath.Clean(filepath.Dir(path)) != filepath.Clean(dir) || !strings.HasPrefix(file, "mediamtx-upload-") {
		return nil, fmt.Errorf("uploaded file does not belong to its recorded directory")
	}
	ext := strings.ToLower(filepath.Ext(file))
	id := strings.TrimSuffix(strings.TrimPrefix(file, "mediamtx-upload-"), ext)
	if _, err = uuid.Parse(id); err != nil || (ext != ".mp4" && ext != ".mkv") {
		return nil, fmt.Errorf("invalid managed upload filename")
	}
	root, err := os.OpenRoot(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	info, err := root.Lstat(file)
	if os.IsNotExist(err) {
		root.Close()
		return nil, nil
	}
	if err != nil {
		root.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		root.Close()
		return nil, fmt.Errorf("managed upload must be a regular file, not a symlink")
	}
	for otherName, c := range p.apiConfigSnapshot().Paths {
		if otherName == name || !strings.HasPrefix(c.Source, "file://") {
			continue
		}
		other, er := fileurl.Path(c.Source)
		if er != nil {
			continue
		}
		otherInfo, er := os.Stat(other)
		if filepath.Clean(other) == filepath.Clean(path) || (er == nil && os.SameFile(info, otherInfo)) {
			root.Close()
			return nil, fmt.Errorf("uploaded file is still referenced by another source")
		}
	}
	tombstone := ".deleting-" + uuid.NewString() + ext
	if err = root.Rename(file, tombstone); err != nil {
		root.Close()
		return nil, fmt.Errorf("cannot remove uploaded file: %w", err)
	}
	return &uploadDeletion{root, file, tombstone}, nil
}

func (d *uploadDeletion) rollback() error {
	if d == nil {
		return nil
	}
	return d.root.Rename(d.tombstone, d.name)
}
