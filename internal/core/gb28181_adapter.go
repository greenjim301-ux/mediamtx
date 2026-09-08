package core

import (
	"context"

	"github.com/bluenviron/mediamtx/internal/servers/gb28181"
	ssgb28181 "github.com/bluenviron/mediamtx/internal/staticsources/gb28181"
)

// gb28181StaticSourceServer adapts the GB28181 server to the interface
// required by the GB28181 static source.
type gb28181StaticSourceServer struct {
	server *gb28181.Server
}

// StartSession implements ssgb28181.Server.
func (w *gb28181StaticSourceServer) StartSession(
	ctx context.Context,
	channelID string,
) (ssgb28181.Session, error) {
	return w.server.StartSession(ctx, channelID)
}

func (p *Core) gb28181StaticSourceServer() ssgb28181.Server {
	if p.gb28181Server == nil {
		return nil
	}
	return &gb28181StaticSourceServer{server: p.gb28181Server}
}
