package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/protocols/gb28181"
)

// GB28181PTZRequest is the request of the PTZ control endpoint.
type GB28181PTZRequest struct {
	// Command is the name of the command (up, down, left, right, zoomin, ...).
	Command string `json:"command"`
	// Speed is the speed of the movement (0-255).
	Speed *int `json:"speed"`
	// Preset is the number of the preset (0-255), used by preset commands.
	Preset *int `json:"preset"`
	// Raw allows to send a command word directly, in hexadecimal form.
	Raw string `json:"raw"`
}

func (a *API) onGB28181DevicesList(ctx *gin.Context) {
	data, err := a.GB28181Server.APIDevicesList()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	data.ItemCount = len(data.Items)
	pageCount, err := paginate(&data.Items, ctx.Query("itemsPerPage"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	data.PageCount = pageCount

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onGB28181DevicesGet(ctx *gin.Context) {
	data, err := a.GB28181Server.APIDevicesGet(ctx.Param("id"))
	if err != nil {
		if errors.Is(err, defs.ErrGB28181DeviceNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onGB28181ChannelsList(ctx *gin.Context) {
	data, err := a.GB28181Server.APIChannelsList()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	data.ItemCount = len(data.Items)
	pageCount, err := paginate(&data.Items, ctx.Query("itemsPerPage"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	data.PageCount = pageCount

	ctx.JSON(http.StatusOK, data)
}

func (a *API) onGB28181DevicesRefresh(ctx *gin.Context) {
	err := a.GB28181Server.APIRefreshCatalog(ctx.Param("id"))
	if err != nil {
		if errors.Is(err, defs.ErrGB28181DeviceNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	a.writeOK(ctx)
}

func (a *API) onGB28181PTZ(ctx *gin.Context) {
	var req GB28181PTZRequest
	err := ctx.ShouldBindJSON(&req)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	var cmd string

	switch {
	case req.Raw != "":
		if !gb28181.IsRawPTZCommand(req.Raw) {
			a.writeError(ctx, http.StatusBadRequest,
				fmt.Errorf("'raw' must be a 16-character hexadecimal command word"))
			return
		}
		cmd = req.Raw

	case req.Command != "":
		speed := 100
		if req.Speed != nil {
			speed = *req.Speed
		}
		if speed < 0 || speed > 255 {
			a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("'speed' must be between 0 and 255"))
			return
		}

		preset := 0
		if req.Preset != nil {
			preset = *req.Preset
		}
		if preset < 0 || preset > 255 {
			a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("'preset' must be between 0 and 255"))
			return
		}

		cmd, err = gb28181.EncodePTZCommand(
			gb28181.PTZCommand(req.Command),
			uint8(speed),  //nolint:gosec
			uint8(preset)) //nolint:gosec
		if err != nil {
			a.writeError(ctx, http.StatusBadRequest, err)
			return
		}

	default:
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("'command' or 'raw' must be provided"))
		return
	}

	err = a.GB28181Server.APIPTZControl(ctx.Param("id"), cmd)
	if err != nil {
		if errors.Is(err, defs.ErrGB28181ChannelNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	a.writeOK(ctx)
}
