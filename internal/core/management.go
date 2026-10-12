package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/fileurl"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/goccy/go-yaml"
)

type managementReq struct {
	change conf.ManagementChange
	res    chan error
}

// APIManagementInfo reports whether durable management is available.
func (p *Core) APIManagementInfo() (string, bool) { return p.confPath, p.confPath != "" }

// APIManagementApply serializes durable changes with all other configuration updates.
func (p *Core) APIManagementApply(change conf.ManagementChange) error {
	res := make(chan error)
	select {
	case p.chManagement <- managementReq{change, res}:
		return <-res
	case <-p.ctx.Done():
		return fmt.Errorf("terminated")
	}
}

func jsonMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = json.Unmarshal(b, &out)
	return out, err
}

func (p *Core) doManagement(change conf.ManagementChange) (*conf.Conf, error) {
	if p.confPath == "" {
		return nil, fmt.Errorf("durable management requires a configuration file")
	}
	path, err := filepath.EvalSymlinks(p.confPath)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0222 == 0 {
		return nil, fmt.Errorf("configuration file is not writable")
	}
	var doc map[string]any
	comments := yaml.CommentMap{}
	if err = yaml.UnmarshalWithOptions(raw, &doc, yaml.Strict(), yaml.CommentToMap(comments)); err != nil {
		return nil, fmt.Errorf("configuration file cannot be edited: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	var deleteCurrent *conf.Path
	switch change.Action {
	case "add", "upload":
		if change.Action == "upload" {
			if change.UploadDirectory == "" || change.UploadFileName == "" {
				return nil, fmt.Errorf("use the upload endpoint to upload a video file")
			}
			change.Path, err = uploadedPath(change)
			if err != nil {
				return nil, err
			}
		}
		if change.Name == "" || strings.HasPrefix(change.Name, "~") || change.Name == "all" || change.Name == "all_others" || change.Path == nil {
			return nil, fmt.Errorf("a named source is required")
		}
		patch, er := jsonMap(change.Path)
		if er != nil {
			return nil, er
		}
		for k := range patch {
			if k != "source" && k != "rtspTransport" && k != "sourceOnDemand" && !(change.Action == "upload" && (k == "fileUploadDirectory" || k == "fileUploadOriginalName")) {
				return nil, fmt.Errorf("unsupported source setting %s", k)
			}
		}
		source, _ := patch["source"].(string)
		if !strings.HasPrefix(source, "rtsp://") && !strings.HasPrefix(source, "rtsps://") && !strings.HasPrefix(source, "file://") && !strings.HasPrefix(source, "gb28181://") {
			return nil, fmt.Errorf("source must use RTSP, file or GB28181")
		}
		if strings.HasPrefix(source, "file://") {
			filePath, er := fileurl.Path(source)
			if er != nil {
				return nil, er
			}
			info, er := os.Stat(filePath)
			if er != nil {
				return nil, fmt.Errorf("cannot read video file: %w", er)
			}
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("video source must be a regular file")
			}
		}
		if _, exists := p.conf.Load().OptionalPaths[change.Name]; exists {
			return nil, fmt.Errorf("path already exists")
		}
		paths, ok := doc["paths"].(map[string]any)
		if !ok {
			if doc["paths"] != nil {
				return nil, fmt.Errorf("invalid paths mapping")
			}
			paths = map[string]any{}
			doc["paths"] = paths
		}
		if _, exists := paths[change.Name]; exists {
			return nil, fmt.Errorf("path already exists in configuration file")
		}
		paths[change.Name] = patch
	case "delete":
		paths, ok := doc["paths"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("path not found in configuration file")
		}
		if _, exists := paths[change.Name]; !exists {
			return nil, fmt.Errorf("path not found in configuration file")
		}
		current, exists := p.conf.Load().Paths[change.Name]
		if !exists || (!strings.HasPrefix(current.Source, "rtsp://") && !strings.HasPrefix(current.Source, "rtsps://") && !strings.HasPrefix(current.Source, "file://") && !strings.HasPrefix(current.Source, "gb28181://")) {
			return nil, fmt.Errorf("only configured RTSP, file or GB28181 sources can be deleted")
		}
		deleteCurrent = current
		delete(paths, change.Name)
	case "global", "storage":
		if change.Global == nil {
			return nil, fmt.Errorf("global settings are required")
		}
		patch, er := jsonMap(change.Global)
		if er != nil {
			return nil, er
		}
		for k, val := range patch {
			if (change.Action == "global" && !strings.HasPrefix(k, "gb28181")) || (change.Action == "storage" && k != "apiFileUploadDirectory" && k != "apiFileUploadMaxSize") {
				return nil, fmt.Errorf("unsupported management setting %s", k)
			}
			doc[k] = val
		}
	default:
		return nil, fmt.Errorf("invalid management action")
	}
	encoded, err := yaml.MarshalWithOptions(doc, yaml.WithComment(comments))
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mediamtx-management-*.yml")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(st.Mode().Perm()); err != nil {
		tmp.Close()
		return nil, err
	}
	if _, err = tmp.Write(encoded); err != nil {
		tmp.Close()
		return nil, err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return nil, err
	}
	if err = tmp.Close(); err != nil {
		return nil, err
	}
	// Validate through the normal loader, including environment overrides.
	next, _, err := conf.Load(tmpName, nil, p.logger)
	if err != nil {
		return nil, err
	}
	if change.Action == "global" {
		check := next.Clone()
		check.GB28181 = true
		if err := check.Validate(nil); err != nil {
			return nil, err
		}
	}
	latest, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(raw, latest) {
		return nil, fmt.Errorf("configuration file changed concurrently; refresh and retry")
	}

	var deletion *uploadDeletion
	if deleteCurrent != nil {
		deletion, err = p.stageUploadDeletion(change.Name, deleteCurrent)
		if err != nil {
			return nil, err
		}
		if deletion != nil {
			defer deletion.root.Close()
		}
	}
	if err = os.Rename(tmpName, path); err != nil {
		if restoreErr := deletion.rollback(); restoreErr != nil {
			return nil, fmt.Errorf("configuration save failed (%v), file recovery failed (%v)", err, restoreErr)
		}
		return nil, fmt.Errorf("cannot save configuration: %w", err)
	}
	if deletion != nil {
		if err = deletion.root.Remove(deletion.tombstone); err != nil {
			fileErr := deletion.rollback()
			confErr := restoreConfigurationFile(path, raw, st.Mode().Perm())
			return nil, fmt.Errorf("cannot delete uploaded file: %v (file recovery: %v; configuration recovery: %v)", err, fileErr, confErr)
		}
	}

	p.Log(logger.Info, "configuration saved by administration UI")
	return next, nil
}

func restoreConfigurationFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".mediamtx-recover-*.yml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
