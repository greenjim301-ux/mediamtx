// Package gb28181 contains a GB/T 28181 server.
package gb28181

import (
	"context"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/protocols/gb28181"
)

type serverParent interface {
	logger.Writer
}

// defaultIP returns the first non-loopback IPv4 address of the machine.
// It is used when the SIP IP is not set explicitly.
func defaultIP() (string, error) {
	intfs, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, intf := range intfs {
		if (intf.Flags&net.FlagUp) == 0 || (intf.Flags&net.FlagLoopback) != 0 {
			continue
		}

		addrs, err2 := intf.Addrs()
		if err2 != nil {
			continue
		}

		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				if ipv4 := ipnet.IP.To4(); ipv4 != nil {
					return ipv4.String(), nil
				}
			}
		}
	}

	return "", fmt.Errorf("unable to detect a local IP address, set 'gb28181SIPIP' manually")
}

// pathName returns the path that contains the stream of a channel.
func pathName(template string, channelID string) string {
	return strings.ReplaceAll(template, "$CHANNEL", channelID)
}

// Server is a GB/T 28181 server.
type Server struct {
	Address         string
	Transports      []string
	Serial          string
	Realm           string
	Password        string
	SIPIP           string
	MediaListenIP   string
	MediaIP         string
	MediaProtocol   string
	MediaPortMin    int
	MediaPortMax    int
	KeepalivePeriod conf.Duration
	PathTemplate    string
	ReadTimeout     conf.Duration
	WriteTimeout    conf.Duration
	Parent          serverParent

	ctx       context.Context
	ctxCancel func()
	wg        sync.WaitGroup

	ua        *sipgo.UserAgent
	sipServer *sipgo.Server
	sipClient *sipgo.Client
	sipPort   int
	ports     *portAllocator
	listeners []io.Closer

	mutex    sync.RWMutex
	devices  map[string]*device
	sessions map[string]*Session

	ssrcCounter uint32
}

// Initialize initializes the server.
func (s *Server) Initialize() error {
	s.ctx, s.ctxCancel = context.WithCancel(context.Background())
	s.devices = make(map[string]*device)
	s.sessions = make(map[string]*Session)

	_, portStr, err := net.SplitHostPort(s.Address)
	if err != nil {
		s.ctxCancel()
		return err
	}
	s.sipPort, err = strconv.Atoi(portStr)
	if err != nil {
		s.ctxCancel()
		return err
	}

	if s.SIPIP == "" {
		s.SIPIP, err = defaultIP()
		if err != nil {
			s.ctxCancel()
			return err
		}
		s.Log(logger.Info, "'gb28181SIPIP' is not set, using %s", s.SIPIP)
	}

	s.ports = &portAllocator{min: s.MediaPortMin, max: s.MediaPortMax}
	s.ports.initialize()

	s.ua, err = sipgo.NewUA(
		sipgo.WithUserAgent("mediamtx"),
		sipgo.WithUserAgentHostname(s.SIPIP),
	)
	if err != nil {
		s.ctxCancel()
		return err
	}

	s.sipServer, err = sipgo.NewServer(s.ua)
	if err != nil {
		s.ua.Close() //nolint:errcheck
		s.ctxCancel()
		return err
	}

	s.sipClient, err = sipgo.NewClient(s.ua,
		sipgo.WithClientHostname(s.SIPIP),
		sipgo.WithClientPort(s.sipPort),
	)
	if err != nil {
		s.ua.Close() //nolint:errcheck
		s.ctxCancel()
		return err
	}

	s.sipServer.OnRegister(s.onRegister)
	s.sipServer.OnMessage(s.onMessage)
	s.sipServer.OnNotify(s.onMessage)
	s.sipServer.OnBye(s.onBye)
	s.sipServer.OnOptions(s.onOptions)

	for _, transport := range s.Transports {
		// make sure that the listener is up before returning,
		// in order to report errors to the user.
		var ln net.Listener
		var pc net.PacketConn

		switch transport {
		case "udp":
			pc, err = net.ListenPacket("udp", s.Address)

		case "tcp":
			ln, err = net.Listen("tcp", s.Address)

		default:
			err = fmt.Errorf("invalid transport: '%s'", transport)
		}

		if err != nil {
			s.closeInner()
			return err
		}

		if pc != nil {
			s.listeners = append(s.listeners, pc)
		} else {
			s.listeners = append(s.listeners, ln)
		}

		s.wg.Go(func() {
			var err2 error
			if pc != nil {
				err2 = s.sipServer.ServeUDP(pc)
			} else {
				err2 = s.sipServer.ServeTCP(ln)
			}

			if err2 != nil && s.ctx.Err() == nil {
				s.Log(logger.Error, "%s listener error: %v", transport, err2)
			}
		})
	}

	s.Log(logger.Info, "started with listener on %s (%s), server ID %s",
		s.Address, strings.Join(s.Transports, ", "), s.Serial)

	return nil
}

func (s *Server) closeInner() {
	s.ctxCancel()

	s.mutex.Lock()
	sessions := make([]*Session, 0, len(s.sessions))
	for _, sx := range s.sessions {
		sessions = append(sessions, sx)
	}
	s.mutex.Unlock()

	for _, sx := range sessions {
		sx.Close()
	}

	if s.sipServer != nil {
		s.sipServer.Close() //nolint:errcheck
	}

	// listeners are created by this server, therefore they must be closed by it.
	for _, ln := range s.listeners {
		ln.Close()
	}
	s.listeners = nil
	if s.sipClient != nil {
		s.sipClient.Close() //nolint:errcheck
	}
	if s.ua != nil {
		s.ua.Close() //nolint:errcheck
	}

	s.wg.Wait()
}

// Close closes the server.
func (s *Server) Close() {
	s.Log(logger.Info, "closing")
	s.closeInner()
}

// Log implements logger.Writer.
func (s *Server) Log(level logger.Level, format string, args ...any) {
	s.Parent.Log(level, "[GB28181] "+format, args...)
}

func (s *Server) mediaProtocol() mediaProtocol {
	if s.MediaProtocol == "tcp" {
		return mediaProtocolTCP
	}
	return mediaProtocolUDP
}

func (s *Server) mediaAdvertisedIP() string {
	if s.MediaIP != "" {
		return s.MediaIP
	}
	return s.SIPIP
}

// generateSSRC generates a SSRC, as defined by GB/T 28181 appendix D.
func (s *Server) generateSSRC() string {
	s.mutex.Lock()
	s.ssrcCounter++
	counter := s.ssrcCounter
	s.mutex.Unlock()

	realm := s.Serial
	if len(realm) >= 8 {
		realm = realm[3:8]
	} else {
		realm = "00000"
	}

	return fmt.Sprintf("0%s%04d", realm, counter%10000)
}

func (s *Server) addSession(sx *Session) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.sessions[sx.channelID] = sx
}

func (s *Server) removeSession(sx *Session) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if cur, ok := s.sessions[sx.channelID]; ok && cur == sx {
		delete(s.sessions, sx.channelID)
	}
}

func (s *Server) findDevice(id string) (*device, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	dev, ok := s.devices[id]
	if !ok {
		return nil, defs.ErrGB28181DeviceNotFound
	}

	return dev, nil
}

// findChannel returns the channel with the given ID, and the device it belongs to.
func (s *Server) findChannel(id string) (*device, *channel, error) {
	s.mutex.RLock()
	devices := make([]*device, 0, len(s.devices))
	for _, dev := range s.devices {
		devices = append(devices, dev)
	}
	s.mutex.RUnlock()

	for _, dev := range devices {
		if ch, ok := dev.getChannel(id); ok {
			return dev, ch, nil
		}
	}

	// devices with a single channel are sometimes addressed with their own ID.
	for _, dev := range devices {
		if dev.id == id {
			return dev, nil, nil
		}
	}

	return nil, nil, defs.ErrGB28181ChannelNotFound
}

func (s *Server) getOrCreateDevice(id string) *device {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	dev, ok := s.devices[id]
	if !ok {
		dev = &device{id: id}
		dev.initialize()
		s.devices[id] = dev
	}

	return dev
}

// onRegister handles the registration of devices.
func (s *Server) onRegister(req *sip.Request, tx sip.ServerTransaction) {
	from := req.From()
	if from == nil || from.Address.User == "" {
		s.respond(tx, req, sip.StatusBadRequest, "Bad Request")
		return
	}

	deviceID := from.Address.User

	expires := 3600
	if h := req.GetHeader("Expires"); h != nil {
		v, err := strconv.Atoi(h.Value())
		if err == nil {
			expires = v
		}
	}

	if s.Password != "" {
		ok := s.checkAuth(req, tx, deviceID)
		if !ok {
			return
		}
	}

	res := sip.NewResponseFromRequest(req, sip.StatusOK, "OK", nil)
	res.AppendHeader(sip.NewHeader("Expires", strconv.Itoa(expires)))
	res.AppendHeader(sip.NewHeader("Date", time.Now().Format("2006-01-02T15:04:05.000")))

	err := tx.Respond(res)
	if err != nil {
		s.Log(logger.Error, "unable to respond to REGISTER: %v", err)
		return
	}

	if expires == 0 {
		s.Log(logger.Info, "device '%s' unregistered", deviceID)

		s.mutex.Lock()
		delete(s.devices, deviceID)
		s.mutex.Unlock()
		return
	}

	dev := s.getOrCreateDevice(deviceID)
	dev.setRegistered(req.Source(), req.Transport(), time.Duration(expires)*time.Second)

	s.Log(logger.Info, "device '%s' registered from %s (%s)", deviceID, req.Source(), req.Transport())

	// query device information and channels.
	s.wg.Go(func() {
		s.queryDevice(dev)
	})
}

func (s *Server) checkAuth(req *sip.Request, tx sip.ServerTransaction, deviceID string) bool {
	h := req.GetHeader("Authorization")
	if h == nil {
		chal := digest.Challenge{
			Realm:     s.Realm,
			Nonce:     strconv.FormatInt(time.Now().UnixNano(), 16),
			Algorithm: "MD5",
			QOP:       []string{"auth"},
		}

		res := sip.NewResponseFromRequest(req, sip.StatusUnauthorized, "Unauthorized", nil)
		res.AppendHeader(sip.NewHeader("WWW-Authenticate", chal.String()))

		err := tx.Respond(res)
		if err != nil {
			s.Log(logger.Error, "unable to respond to REGISTER: %v", err)
		}

		return false
	}

	cred, err := digest.ParseCredentials(h.Value())
	if err != nil {
		s.Log(logger.Warn, "device '%s': invalid credentials: %v", deviceID, err)
		s.respond(tx, req, sip.StatusBadRequest, "Bad credentials")
		return false
	}

	if cred.Username != deviceID {
		s.Log(logger.Warn, "device '%s': username mismatch", deviceID)
		s.respond(tx, req, sip.StatusForbidden, "Invalid username")
		return false
	}

	expected, err := digest.Digest(&digest.Challenge{
		Realm:     cred.Realm,
		Nonce:     cred.Nonce,
		Opaque:    cred.Opaque,
		Algorithm: cred.Algorithm,
		QOP:       []string{cred.QOP},
	}, digest.Options{
		Method:   string(req.Method),
		URI:      cred.URI,
		Username: deviceID,
		Password: s.Password,
		Cnonce:   cred.Cnonce,
		Count:    cred.Nc,
	})
	if err != nil {
		s.Log(logger.Warn, "device '%s': unable to compute digest: %v", deviceID, err)
		s.respond(tx, req, sip.StatusUnauthorized, "Unauthorized")
		return false
	}

	if expected.Response != cred.Response {
		s.Log(logger.Warn, "device '%s': authentication failed", deviceID)
		s.respond(tx, req, sip.StatusForbidden, "Forbidden")
		return false
	}

	return true
}

// onMessage handles MESSAGE and NOTIFY requests, that carry MANSCDP messages.
func (s *Server) onMessage(req *sip.Request, tx sip.ServerTransaction) {
	// devices expect a reply as soon as possible.
	s.respond(tx, req, sip.StatusOK, "OK")

	body := req.Body()
	if len(body) == 0 {
		return
	}

	msg, err := gb28181.DecodeMessage(body)
	if err != nil {
		s.Log(logger.Debug, "unable to decode message: %v", err)
		return
	}

	deviceID := msg.DeviceID
	if from := req.From(); from != nil && from.Address.User != "" {
		deviceID = from.Address.User
	}

	if deviceID == "" {
		return
	}

	switch msg.CmdType {
	case gb28181.CmdTypeKeepalive:
		dev, err2 := s.findDevice(deviceID)
		if err2 != nil {
			return
		}
		dev.setKeepalive()

	case gb28181.CmdTypeCatalog:
		dev, err2 := s.findDevice(deviceID)
		if err2 != nil {
			return
		}

		dev.setChannels(msg.DeviceList.Items)

		s.Log(logger.Info, "device '%s': received %d channels", deviceID, len(msg.DeviceList.Items))

	case gb28181.CmdTypeDeviceInfo:
		dev, err2 := s.findDevice(deviceID)
		if err2 != nil {
			return
		}
		dev.setInfo(msg)

	default:
		s.Log(logger.Debug, "device '%s': received message '%s'", deviceID, msg.CmdType)
	}
}

// onBye handles the termination of a session by a device.
func (s *Server) onBye(req *sip.Request, tx sip.ServerTransaction) {
	s.respond(tx, req, sip.StatusOK, "OK")

	callID := req.CallID()
	if callID == nil {
		return
	}

	s.mutex.RLock()
	var found *Session
	for _, sx := range s.sessions {
		if sx.callID == callID.Value() {
			found = sx
			break
		}
	}
	s.mutex.RUnlock()

	if found != nil {
		s.Log(logger.Info, "channel '%s': stream terminated by device", found.channelID)
		found.receiver.pipe.closeWithError(fmt.Errorf("stream terminated by device"))
	}
}

func (s *Server) onOptions(req *sip.Request, tx sip.ServerTransaction) {
	s.respond(tx, req, sip.StatusOK, "OK")
}

func (s *Server) respond(tx sip.ServerTransaction, req *sip.Request, code int, reason string) {
	err := tx.Respond(sip.NewResponseFromRequest(req, code, reason, nil))
	if err != nil {
		s.Log(logger.Debug, "unable to respond to %s: %v", req.Method, err)
	}
}

// queryDevice asks a device for its information and its channels.
func (s *Server) queryDevice(dev *device) {
	ctx, cancel := context.WithTimeout(s.ctx, time.Duration(s.WriteTimeout)*2)
	defer cancel()

	err := s.sendQuery(ctx, dev, gb28181.CmdTypeDeviceInfo)
	if err != nil {
		s.Log(logger.Debug, "device '%s': unable to query device info: %v", dev.id, err)
	}

	err = s.sendQuery(ctx, dev, gb28181.CmdTypeCatalog)
	if err != nil {
		s.Log(logger.Warn, "device '%s': unable to query catalog: %v", dev.id, err)
	}
}

// sendQuery sends a MANSCDP query to a device.
func (s *Server) sendQuery(ctx context.Context, dev *device, cmdType string) error {
	body := gb28181.EncodeQuery(cmdType, dev.nextSN(), dev.id)
	return s.sendMessage(ctx, dev, dev.id, body)
}

// sendMessage sends a MESSAGE request to a device.
func (s *Server) sendMessage(ctx context.Context, dev *device, target string, body []byte) error {
	addr, transport := dev.target()
	if addr == "" {
		return fmt.Errorf("device '%s' is not registered", dev.id)
	}

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	devPort, _ := strconv.Atoi(portStr)

	req := sip.NewRequest(sip.MESSAGE, sip.Uri{
		Scheme: "sip",
		User:   target,
		Host:   host,
		Port:   devPort,
	})

	from := &sip.FromHeader{
		Address: sip.Uri{Scheme: "sip", User: s.Serial, Host: s.Realm},
		Params:  sip.NewParams(),
	}
	from.Params.Add("tag", sip.GenerateTagN(16))
	req.AppendHeader(from)

	req.AppendHeader(&sip.ToHeader{
		Address: sip.Uri{Scheme: "sip", User: target, Host: s.Realm},
	})

	req.AppendHeader(sip.NewHeader("Content-Type", "Application/MANSCDP+xml"))
	req.SetBody(body)

	req.SetDestination(addr)
	req.SetTransport(strings.ToUpper(transport))

	res, err := s.sipClient.Do(ctx, req)
	if err != nil {
		return err
	}

	if res.StatusCode != sip.StatusOK {
		return fmt.Errorf("device replied with code %d (%s)", res.StatusCode, res.Reason)
	}

	return nil
}

// APIDevicesList returns all known devices.
func (s *Server) APIDevicesList() (*defs.APIGB28181DeviceList, error) {
	s.mutex.RLock()
	devices := make([]*device, 0, len(s.devices))
	for _, dev := range s.devices {
		devices = append(devices, dev)
	}
	s.mutex.RUnlock()

	data := &defs.APIGB28181DeviceList{
		Items: []defs.APIGB28181Device{},
	}

	for _, dev := range devices {
		data.Items = append(data.Items, *dev.apiItem(time.Duration(s.KeepalivePeriod), s.PathTemplate))
	}

	sort.Slice(data.Items, func(i, j int) bool {
		return data.Items[i].ID < data.Items[j].ID
	})

	data.ItemCount = len(data.Items)
	data.PageCount = 1

	return data, nil
}

// APIDevicesGet returns a single device.
func (s *Server) APIDevicesGet(id string) (*defs.APIGB28181Device, error) {
	dev, err := s.findDevice(id)
	if err != nil {
		return nil, err
	}

	return dev.apiItem(time.Duration(s.KeepalivePeriod), s.PathTemplate), nil
}

// APIChannelsList returns all channels of all devices.
func (s *Server) APIChannelsList() (*defs.APIGB28181ChannelList, error) {
	s.mutex.RLock()
	devices := make([]*device, 0, len(s.devices))
	for _, dev := range s.devices {
		devices = append(devices, dev)
	}
	s.mutex.RUnlock()

	data := &defs.APIGB28181ChannelList{
		Items: []defs.APIGB28181Channel{},
	}

	for _, dev := range devices {
		dev.mutex.RLock()
		for _, ch := range dev.channels {
			data.Items = append(data.Items, *ch.apiItem(s.PathTemplate))
		}
		dev.mutex.RUnlock()
	}

	sort.Slice(data.Items, func(i, j int) bool {
		return data.Items[i].ID < data.Items[j].ID
	})

	data.ItemCount = len(data.Items)
	data.PageCount = 1

	return data, nil
}

// APIRefreshCatalog asks a device for its channels.
func (s *Server) APIRefreshCatalog(deviceID string) error {
	dev, err := s.findDevice(deviceID)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(s.ctx, time.Duration(s.WriteTimeout)*2)
	defer cancel()

	return s.sendQuery(ctx, dev, gb28181.CmdTypeCatalog)
}

// APIPTZControl sends a PTZ command to a channel.
func (s *Server) APIPTZControl(channelID string, ptzCmd string) error {
	dev, _, err := s.findChannel(channelID)
	if err != nil {
		return err
	}

	body := gb28181.EncodeDeviceControlPTZ(dev.nextSN(), channelID, ptzCmd)

	ctx, cancel := context.WithTimeout(s.ctx, time.Duration(s.WriteTimeout)*2)
	defer cancel()

	return s.sendMessage(ctx, dev, channelID, body)
}
