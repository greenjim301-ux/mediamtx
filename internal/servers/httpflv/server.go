// Package httpflv contains a HTTP-FLV server.
package httpflv

import (
	"context"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/externalcmd"
	"github.com/bluenviron/mediamtx/internal/logger"
)

type serverPathManager interface {
	AddReader(req defs.PathAddReaderReq) (*defs.PathAddReaderRes, error)
}

type serverParent interface {
	logger.Writer
}

// Server is a HTTP-FLV server.
type Server struct {
	Address         string
	DumpPackets     bool
	Encryption      bool
	ServerKey       string
	ServerCert      string
	AllowOrigins    []string
	TrustedProxies  conf.IPNetworks
	ReadTimeout     conf.Duration
	WriteTimeout    conf.Duration
	ExternalCmdPool *externalcmd.Pool
	PathManager     serverPathManager
	Parent          serverParent

	ctx        context.Context
	ctxCancel  func()
	httpServer *httpServer
}

// Initialize initializes the server.
func (s *Server) Initialize() error {
	s.ctx, s.ctxCancel = context.WithCancel(context.Background())

	s.httpServer = &httpServer{
		address:         s.Address,
		dumpPackets:     s.DumpPackets,
		encryption:      s.Encryption,
		serverKey:       s.ServerKey,
		serverCert:      s.ServerCert,
		allowOrigins:    s.AllowOrigins,
		trustedProxies:  s.TrustedProxies,
		readTimeout:     s.ReadTimeout,
		writeTimeout:    s.WriteTimeout,
		externalCmdPool: s.ExternalCmdPool,
		pathManager:     s.PathManager,
		parent:          s,
	}
	err := s.httpServer.initialize()
	if err != nil {
		s.ctxCancel()
		return err
	}

	str := "started with listener on " + s.Address
	if !s.Encryption {
		str += " (TCP/HTTP)"
	} else {
		str += " (TCP/HTTPS)"
	}
	s.Log(logger.Info, str)

	return nil
}

// Log implements logger.Writer.
func (s *Server) Log(level logger.Level, format string, args ...any) {
	s.Parent.Log(level, "[HTTP-FLV] "+format, args...)
}

// Close closes the server.
func (s *Server) Close() {
	s.Log(logger.Info, "closing")
	s.ctxCancel()
	s.httpServer.close()
}
