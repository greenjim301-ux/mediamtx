package gb28181

import (
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/protocols/gb28181"
)

// channel is a channel (camera) of a device.
type channel struct {
	item     gb28181.CatalogItem
	updated  time.Time
	deviceID string
}

// device is a registered GB28181 device.
type device struct {
	mutex sync.RWMutex

	id        string
	addr      string // address to which requests are sent
	transport string

	registeredAt  time.Time
	expiresAt     time.Time
	lastKeepalive time.Time

	name         string
	manufacturer string
	model        string
	firmware     string
	deviceType   string

	channels map[string]*channel

	sn int
}

func (d *device) initialize() {
	d.channels = make(map[string]*channel)
}

func (d *device) nextSN() int {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.sn++
	if d.sn > 999999 {
		d.sn = 1
	}
	return d.sn
}

func (d *device) setRegistered(addr string, transport string, expires time.Duration) {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	now := time.Now()
	d.addr = addr
	d.transport = transport
	d.registeredAt = now
	d.expiresAt = now.Add(expires)
	d.lastKeepalive = now
}

func (d *device) setKeepalive() {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.lastKeepalive = time.Now()
}

func (d *device) setInfo(msg *gb28181.Message) {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	if msg.Manufacturer != "" {
		d.manufacturer = msg.Manufacturer
	}
	if msg.Model != "" {
		d.model = msg.Model
	}
	if msg.Firmware != "" {
		d.firmware = msg.Firmware
	}
	if msg.DeviceType != "" {
		d.deviceType = msg.DeviceType
	}
}

// setChannels merges the items of a catalog response.
func (d *device) setChannels(items []gb28181.CatalogItem) {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	now := time.Now()

	for _, item := range items {
		if item.DeviceID == "" {
			continue
		}

		// some devices insert themselves in the catalog: use it as device name.
		if item.DeviceID == d.id {
			if item.Name != "" {
				d.name = item.Name
			}
			continue
		}

		d.channels[item.DeviceID] = &channel{
			item:     item,
			updated:  now,
			deviceID: d.id,
		}
	}
}

func (d *device) getChannel(id string) (*channel, bool) {
	d.mutex.RLock()
	defer d.mutex.RUnlock()
	ch, ok := d.channels[id]
	return ch, ok
}

func (d *device) target() (string, string) {
	d.mutex.RLock()
	defer d.mutex.RUnlock()
	return d.addr, d.transport
}

func (d *device) apiItem(keepalivePeriod time.Duration, pathTemplate string) *defs.APIGB28181Device {
	d.mutex.RLock()
	defer d.mutex.RUnlock()

	item := &defs.APIGB28181Device{
		ID:           d.id,
		Address:      d.addr,
		Transport:    d.transport,
		Name:         d.name,
		Manufacturer: d.manufacturer,
		Model:        d.model,
		Firmware:     d.firmware,
		DeviceType:   d.deviceType,
		Channels:     []defs.APIGB28181Channel{},
	}

	if !d.registeredAt.IsZero() {
		v := d.registeredAt
		item.RegisteredTime = &v
	}

	if !d.lastKeepalive.IsZero() {
		v := d.lastKeepalive
		item.LastKeepaliveTime = &v
	}

	deadline := d.lastKeepalive.Add(keepalivePeriod)
	if d.expiresAt.After(deadline) {
		deadline = d.expiresAt
	}
	item.Online = !d.registeredAt.IsZero() && time.Now().Before(deadline)

	for _, ch := range d.channels {
		item.Channels = append(item.Channels, *ch.apiItem(pathTemplate))
	}

	return item
}

func (c *channel) apiItem(pathTemplate string) *defs.APIGB28181Channel {
	return &defs.APIGB28181Channel{
		ID:           c.item.DeviceID,
		DeviceID:     c.deviceID,
		Name:         c.item.Name,
		Manufacturer: c.item.Manufacturer,
		Model:        c.item.Model,
		Address:      c.item.Address,
		ParentID:     c.item.ParentID,
		Online:       c.item.IsOnline(),
		PTZType:      c.item.Info.PTZType,
		Longitude:    c.item.Longitude,
		Latitude:     c.item.Latitude,
		Path:         pathName(pathTemplate, c.item.DeviceID),
	}
}
