package api

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/conf/jsonwrapper"
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
	ctx.JSON(200, gin.H{"configFile": path, "persistent": available})
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
