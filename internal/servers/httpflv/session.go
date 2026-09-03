package httpflv

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/externalcmd"
	"github.com/bluenviron/mediamtx/internal/hooks"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/protocols/flv"
	"github.com/bluenviron/mediamtx/internal/protocols/httpp"
	"github.com/bluenviron/mediamtx/internal/stream"
)

// flushWriter flushes the underlying HTTP response writer after every write,
// so that FLV tags reach the client as soon as they are produced.
type flushWriter struct {
	w gin.ResponseWriter
}

func (f flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err != nil {
		return n, err
	}
	f.w.Flush()
	return n, nil
}

type session struct {
	remoteAddr      string
	pathName        string
	externalCmdPool *externalcmd.Pool
	pathManager     serverPathManager
	parent          *Server

	ctx       context.Context
	ctxCancel func()
	uuid      uuid.UUID
}

func (s *session) run(ctx *gin.Context) {
	s.ctx, s.ctxCancel = context.WithCancel(s.parent.ctx)
	s.uuid = uuid.New()

	s.Log(logger.Info, "created by %s", s.remoteAddr)

	err := s.runInner(ctx)

	s.Log(logger.Info, "closed: %v", err)
}

func (s *session) runInner(ctx *gin.Context) error {
	ip, _, _ := net.SplitHostPort(s.remoteAddr)

	res, err := s.pathManager.AddReader(defs.PathAddReaderReq{
		Author: s,
		AccessRequest: defs.PathAccessRequest{
			Name:                 s.pathName,
			Query:                ctx.Request.URL.RawQuery,
			UserAgent:            ctx.Request.UserAgent(),
			Proto:                auth.ProtocolHTTPFLV,
			ID:                   &s.uuid,
			Credentials:          httpp.Credentials(ctx.Request),
			IP:                   net.ParseIP(ip),
			EnableAskCredentials: true,
		},
	})
	if err != nil {
		if terr, ok := errors.AsType[*auth.Error](err); ok {
			if terr.AskCredentials {
				ctx.Header("WWW-Authenticate", `Basic realm="mediamtx"`)
			}
			ctx.AbortWithStatus(http.StatusUnauthorized)
			return err
		}

		if _, ok := errors.AsType[*defs.PathNoStreamAvailableError](err); ok {
			ctx.AbortWithStatus(http.StatusNotFound)
			return err
		}

		ctx.AbortWithStatus(http.StatusBadRequest)
		return err
	}
	defer res.Path.RemoveReader(defs.PathRemoveReaderReq{Author: s})

	r := &stream.Reader{Parent: s}

	w := &flv.Writer{W: flushWriter{ctx.Writer}}

	err = flv.FromStream(res.Stream.OrigDesc, r, w)
	if err != nil {
		ctx.AbortWithStatus(http.StatusNotFound)
		return err
	}

	ctx.Header("Content-Type", "video/x-flv")
	ctx.Header("Cache-Control", "no-cache")
	ctx.Writer.WriteHeader(http.StatusOK)

	err = w.WriteHeader()
	if err != nil {
		return err
	}

	s.Log(logger.Info, "is reading from path '%s', %s", res.Path.Name(), defs.FormatsInfo(r.Formats()))

	onUnreadHook := hooks.OnRead(hooks.OnReadParams{
		Logger:          s,
		ExternalCmdPool: s.externalCmdPool,
		Conf:            res.Path.SafeConf(),
		ExternalCmdEnv:  res.Path.ExternalCmdEnv(),
		Reader:          *s.APIReaderDescribe(),
		Query:           ctx.Request.URL.RawQuery,
	})
	defer onUnreadHook()

	res.Stream.AddReader(r)
	defer res.Stream.RemoveReader(r)

	select {
	case <-ctx.Request.Context().Done():
		return fmt.Errorf("closed by client")

	case <-s.ctx.Done():
		return fmt.Errorf("terminated")

	case readErr := <-r.Error():
		return readErr
	}
}

// Close implements defs.Reader.
func (s *session) Close() {
	s.ctxCancel()
}

// Log implements logger.Writer.
func (s *session) Log(level logger.Level, format string, args ...any) {
	id := hex.EncodeToString(s.uuid[:4])
	s.parent.Log(level, "[httpflv session %v] "+format, append([]any{id}, args...)...)
}

// APIReaderDescribe implements defs.Reader.
func (s *session) APIReaderDescribe() *defs.APIPathReader {
	return &defs.APIPathReader{
		Type: defs.APIPathReaderTypeHTTPFLVSession,
		ID:   s.uuid.String(),
	}
}
