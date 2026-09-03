package httpflv

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/externalcmd"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/protocols/httpp"
)

type httpServer struct {
	address         string
	dumpPackets     bool
	encryption      bool
	serverKey       string
	serverCert      string
	allowOrigins    []string
	trustedProxies  conf.IPNetworks
	readTimeout     conf.Duration
	writeTimeout    conf.Duration
	externalCmdPool *externalcmd.Pool
	pathManager     serverPathManager
	parent          *Server

	inner *httpp.Server
}

func (s *httpServer) initialize() error {
	router := gin.New()
	router.SetTrustedProxies(s.trustedProxies.ToTrustedProxies()) //nolint:errcheck
	router.Use(s.middlewarePreflightRequests)
	router.Use(s.onRequest)

	var proto string
	if s.encryption {
		proto = "httpflvs"
	} else {
		proto = "httpflv"
	}

	s.inner = &httpp.Server{
		Address:           s.address,
		AllowOrigins:      s.allowOrigins,
		DumpPackets:       s.dumpPackets,
		DumpPacketsPrefix: proto + "_server_conn",
		ReadTimeout:       time.Duration(s.readTimeout),
		WriteTimeout:      time.Duration(s.writeTimeout),
		Encryption:        s.encryption,
		ServerCert:        s.serverCert,
		ServerKey:         s.serverKey,
		Handler:           router,
		Parent:            s,
	}
	return s.inner.Initialize()
}

// Log implements logger.Writer.
func (s *httpServer) Log(level logger.Level, format string, args ...any) {
	s.parent.Log(level, format, args...)
}

func (s *httpServer) close() {
	s.inner.Close()
}

func (s *httpServer) middlewarePreflightRequests(ctx *gin.Context) {
	if ctx.Request.Method == http.MethodOptions &&
		ctx.Request.Header.Get("Access-Control-Request-Method") != "" {
		ctx.Header("Access-Control-Allow-Methods", "OPTIONS, GET")
		ctx.Header("Access-Control-Allow-Headers", "Authorization")
		ctx.AbortWithStatus(http.StatusNoContent)
		return
	}
}

// onRequest handles requests of the form GET /<path>.flv
func (s *httpServer) onRequest(ctx *gin.Context) {
	if ctx.Request.Method != http.MethodGet {
		return
	}

	pa := strings.TrimPrefix(ctx.Request.URL.Path, "/")

	if !strings.HasSuffix(pa, ".flv") {
		return
	}

	pa = strings.TrimSuffix(pa, ".flv")
	if pa == "" {
		return
	}

	sx := &session{
		remoteAddr:      httpp.RemoteAddr(ctx),
		pathName:        pa,
		externalCmdPool: s.externalCmdPool,
		pathManager:     s.pathManager,
		parent:          s.parent,
	}
	sx.run(ctx)
}
