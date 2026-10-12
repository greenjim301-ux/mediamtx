package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/conf"
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

	c := a.Parent.APIConfigSnapshot()
	for i := range data.Items {
		for j := range data.Items[i].Channels {
			updateGB28181PathState(&data.Items[i].Channels[j], c.Paths)
		}
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

	c := a.Parent.APIConfigSnapshot()
	for i := range data.Channels {
		updateGB28181PathState(&data.Channels[i], c.Paths)
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
	c := a.Parent.APIConfigSnapshot()
	for i := range data.Items {
		updateGB28181PathState(&data.Items[i], c.Paths)
	}

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

// Report the effective binding using the same matcher as the path manager.
// An explicit path takes precedence over regex rules; fallback publisher paths
// leave room for adding a dedicated source. Never report another channel as ready.
func updateGB28181PathState(channel *defs.APIGB28181Channel, paths map[string]*conf.Path) {
	channel.PathConfigured = false
	channel.PathConflict = false
	channel.PathConfigName = ""
	path, matches, err := conf.FindPathConf(paths, channel.Path)
	if err != nil {
		return
	}
	source := path.Source
	for i := len(matches) - 1; i >= 1; i-- {
		source = strings.ReplaceAll(source, "$G"+strconv.Itoa(i), matches[i])
	}
	source = strings.ReplaceAll(source, "$MTX_QUERY", "")
	if strings.HasPrefix(source, "gb28181://") {
		id := strings.Trim(strings.TrimPrefix(source, "gb28181://"), "/")
		if i := strings.LastIndex(id, "/"); i >= 0 {
			id = id[i+1:]
		}
		channel.PathConfigured = id == channel.ID
	}
	fallback := (path.Name == "all" || path.Name == "all_others") && source == "publisher"
	channel.PathConflict = !channel.PathConfigured && !fallback
	channel.PathConfigName = path.Name
}
