package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/protocols/onvif"
)

// ONVIFProbeRequest is the payload for probing an ONVIF device.
type ONVIFProbeRequest struct {
	IP   string `json:"ip"`
	User string `json:"user"`
	Pass string `json:"pass"`
}

// ONVIFPTZRequest is the payload for controlling an ONVIF PTZ camera.
type ONVIFPTZRequest struct {
	Command string   `json:"command"` // "up", "down", "left", "right", "zoomin", "zoomout", "stop"
	Speed   *float64 `json:"speed"`   // 0.0 to 1.0 (default 0.5)
	Pan     *float64 `json:"pan"`     // optional direct pan velocity
	Tilt    *float64 `json:"tilt"`    // optional direct tilt velocity
	Zoom    *float64 `json:"zoom"`    // optional direct zoom velocity
}

func (a *API) onONVIFProbe(ctx *gin.Context) {
	var req ONVIFProbeRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	req.IP = strings.TrimSpace(req.IP)
	if req.IP == "" {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("device IP is required"))
		return
	}

	cli := onvif.NewClient()
	probeCtx, cancel := context.WithTimeout(ctx.Request.Context(), 15*time.Second)
	defer cancel()

	res, err := cli.Discover(probeCtx, req.IP, req.User, req.Pass)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("ONVIF 探测失败: %w", err))
		return
	}

	ctx.JSON(http.StatusOK, res)
}

func (a *API) onONVIFPTZ(ctx *gin.Context) {
	name, ok := paramName(ctx)
	if !ok {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid path name"))
		return
	}

	var req ONVIFPTZRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	c := a.Parent.APIConfigSnapshot()
	pconf, ok := c.Paths[name]
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("path '%s' not found", name))
		return
	}

	if pconf.ONVIFPTZURL == "" || pconf.ONVIFProfileToken == "" {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("path '%s' does not have ONVIF PTZ configured", name))
		return
	}

	cli := onvif.NewClient()
	ptzCtx, cancel := context.WithTimeout(ctx.Request.Context(), 5*time.Second)
	defer cancel()

	cmd := strings.ToLower(strings.TrimSpace(req.Command))
	if cmd == "stop" {
		err := cli.Stop(ptzCtx, pconf.ONVIFPTZURL, pconf.ONVIFProfileToken, pconf.ONVIFUser, pconf.ONVIFPass, true, true)
		if err != nil {
			a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("PTZ Stop failed: %w", err))
			return
		}
		a.writeOK(ctx)
		return
	}

	speed := 0.5
	if req.Speed != nil {
		speed = *req.Speed
		if speed < 0.05 {
			speed = 0.05
		} else if speed > 1.0 {
			speed = 1.0
		}
	}

	var panX, tiltY, zoom float64
	switch cmd {
	case "up":
		tiltY = speed
	case "down":
		tiltY = -speed
	case "left":
		panX = -speed
	case "right":
		panX = speed
	case "zoomin":
		zoom = speed
	case "zoomout":
		zoom = -speed
	default:
		if req.Pan != nil || req.Tilt != nil || req.Zoom != nil {
			if req.Pan != nil {
				panX = *req.Pan
			}
			if req.Tilt != nil {
				tiltY = *req.Tilt
			}
			if req.Zoom != nil {
				zoom = *req.Zoom
			}
		} else {
			a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("unknown PTZ command '%s'", req.Command))
			return
		}
	}

	err := cli.ContinuousMove(ptzCtx, pconf.ONVIFPTZURL, pconf.ONVIFProfileToken, pconf.ONVIFUser, pconf.ONVIFPass, panX, tiltY, zoom)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("PTZ move failed: %w", err))
		return
	}

	a.writeOK(ctx)
}
