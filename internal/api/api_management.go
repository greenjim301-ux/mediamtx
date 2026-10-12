package api

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/conf/jsonwrapper"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/gin-gonic/gin"
)

// Built with: cd web && npm ci && npm run build.
//
//go:embed webdist
var adminAssets embed.FS

type managementParent interface {
	APIManagementInfo() (string, bool)
	APIManagementApply(conf.ManagementChange) error
}

func (a *API) onManagementInfo(ctx *gin.Context) {
	p, ok := a.Parent.(managementParent)
	if !ok {
		a.writeError(ctx, 501, fmt.Errorf("durable management unavailable"))
		return
	}
	path, available := p.APIManagementInfo()
	info := gin.H{"configFile": path, "persistent": available}
	if upload, ok := a.Parent.(uploadParent); ok {
		dir, maxSize, err := upload.APIManagementUploadSettings()
		if err != nil {
			a.writeError(ctx, 500, err)
			return
		}
		info["uploadDirectory"] = dir
		info["maxUploadSize"] = maxSize
	}
	ctx.JSON(200, info)
}

func (a *API) onManagementApply(ctx *gin.Context) {
	p, ok := a.Parent.(managementParent)
	if !ok {
		a.writeError(ctx, 501, fmt.Errorf("durable management unavailable"))
		return
	}
	var change conf.ManagementChange
	if err := jsonwrapper.Decode(&customLimitReader{ctx.Request.Body, maxInboundConfigSize}, &change); err != nil {
		a.writeError(ctx, 400, err)
		return
	}
	if err := p.APIManagementApply(change); err != nil {
		a.writeError(ctx, 400, err)
		return
	}
	a.writeOK(ctx)
}

func (a *API) onPreview(ctx *gin.Context) {
	if interfaceIsEmpty(a.HTTPFLVServer) {
		a.writeError(ctx, 503, fmt.Errorf("HTTP-FLV server is disabled"))
		return
	}
	path := ctx.Param("name")
	if !strings.HasSuffix(path, ".flv") {
		a.writeError(ctx, 400, fmt.Errorf("preview requires a .flv path"))
		return
	}
	r := ctx.Request.Clone(ctx.Request.Context())
	u := *r.URL
	u.Path = path
	u.RawPath = ""
	r.URL = &u
	// Reuse the real HTTP-FLV handler, including per-path read authentication.
	// No reverse proxy, extra connection, TLS downgrade or CORS exception is needed.
	a.HTTPFLVServer.ServeHTTP(ctx.Writer, r)
}

func mountAdmin(router *gin.Engine) {
	sub, _ := fs.Sub(adminAssets, "webdist")
	handler := http.StripPrefix("/admin/", http.FileServer(http.FS(sub)))
	router.GET("/", func(ctx *gin.Context) { ctx.Redirect(http.StatusTemporaryRedirect, "/admin/") })
	router.GET("/admin", func(ctx *gin.Context) { ctx.Redirect(http.StatusTemporaryRedirect, "/admin/") })
	router.GET("/admin/*asset", func(ctx *gin.Context) {
		ctx.Header("Cache-Control", "no-cache")
		ctx.Header("X-Content-Type-Options", "nosniff")
		handler.ServeHTTP(ctx.Writer, ctx.Request)
	})
}

type uploadParent interface {
	APIManagementUploadSettings() (string, uint64, error)
	APIManagementUpload(context.Context, string, string, io.Reader) error
}

// singleUploadReader verifies EOF of the complete multipart request before commit.
// A second file or unexpected field invalidates the upload and triggers cleanup.
type singleUploadReader struct {
	part      *multipart.Part
	multipart *multipart.Reader
	done      bool
	mu        sync.Mutex
	closed    bool
	abort     func() error
}

func (r *singleUploadReader) Read(b []byte) (int, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	r.mu.Unlock()
	if r.done {
		return 0, io.EOF
	}
	n, err := r.part.Read(b)
	if err == io.EOF {
		r.done = true
		_, nextErr := r.multipart.NextPart()
		if nextErr == io.EOF {
			return n, io.EOF
		}
		if nextErr != nil {
			return n, nextErr
		}
		return n, fmt.Errorf("upload accepts exactly one file")
	}
	return n, err
}

func (a *API) onFileUpload(ctx *gin.Context) {
	p, ok := a.Parent.(uploadParent)
	if !ok {
		a.writeError(ctx, 501, fmt.Errorf("file uploads unavailable"))
		return
	}
	name, ok := paramName(ctx)
	if !ok {
		a.writeError(ctx, 400, fmt.Errorf("invalid source name"))
		return
	}
	_, limit, err := p.APIManagementUploadSettings()
	if err != nil {
		a.writeError(ctx, 400, err)
		return
	}
	body := &uploadBody{ReadCloser: ctx.Request.Body, controller: http.NewResponseController(ctx.Writer), timeout: time.Duration(a.ReadTimeout)}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, body, int64(limit)+(1<<20))
	mr, err := ctx.Request.MultipartReader()
	if err != nil {
		a.writeError(ctx, 400, err)
		return
	}
	part, err := mr.NextPart()
	if err != nil {
		a.writeError(ctx, 400, err)
		return
	}
	defer part.Close()
	if part.FormName() != "file" || part.FileName() == "" {
		a.writeError(ctx, 400, fmt.Errorf("multipart field file is required"))
		return
	}
	err = p.APIManagementUpload(ctx.Request.Context(), name, part.FileName(), &singleUploadReader{part: part, multipart: mr, abort: body.abort})
	if err != nil {
		var tooLarge *http.MaxBytesError
		status := http.StatusBadRequest
		if errors.Is(err, defs.ErrFileUploadTooLarge) || errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		a.writeError(ctx, status, err)
		return
	}
	a.writeOK(ctx)
}

// Interrupt a blocked body read when the client or service cancels the upload.
func (r *singleUploadReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if r.abort != nil {
		return r.abort()
	}
	return nil
}

// Refresh on every raw body read, not just every decoded multipart frame: the
// multipart decoder can perform many network reads before returning one frame.
type uploadBody struct {
	io.ReadCloser
	controller *http.ResponseController
	timeout    time.Duration
	mu         sync.Mutex
	closed     bool
}

func (b *uploadBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	b.controller.SetReadDeadline(time.Now().Add(b.timeout))
	b.mu.Unlock()
	return b.ReadCloser.Read(p)
}
func (b *uploadBody) abort() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return b.controller.SetReadDeadline(time.Now())
}
