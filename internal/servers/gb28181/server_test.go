package gb28181

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/protocols/mpegps"
	"github.com/bluenviron/mediamtx/internal/test"
)

const (
	testServerAddr = "127.0.0.1:15060"
	testDeviceID   = "34020000001110000001"
	testChannelID  = "34020000001320000001"
	testServerID   = "34020000002000000001"
	testRealm      = "3402000000"
)

// fakeDevice is a minimal GB28181 device, used to test the server.
// It uses an unconnected socket, like real devices do, in order to
// receive requests coming from any source port.
type fakeDevice struct {
	t          *testing.T
	pc         net.PacketConn
	serverAddr *net.UDPAddr
}

func newFakeDevice(t *testing.T) *fakeDevice {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)

	serverAddr, err := net.ResolveUDPAddr("udp", testServerAddr)
	require.NoError(t, err)

	return &fakeDevice{
		t:          t,
		pc:         pc,
		serverAddr: serverAddr,
	}
}

func (d *fakeDevice) close() {
	d.pc.Close()
}

func (d *fakeDevice) localAddr() string {
	return d.pc.LocalAddr().String()
}

// readMessage reads a SIP message.
func (d *fakeDevice) readMessage(timeout time.Duration) (string, error) {
	d.pc.SetReadDeadline(time.Now().Add(timeout)) //nolint:errcheck

	buf := make([]byte, 4096)
	n, _, err := d.pc.ReadFrom(buf)
	if err != nil {
		return "", err
	}

	return string(buf[:n]), nil
}

func (d *fakeDevice) write(msg string) {
	_, err := d.pc.WriteTo([]byte(msg), d.serverAddr)
	require.NoError(d.t, err)
}

// register performs a registration.
func (d *fakeDevice) register() {
	_, port, _ := net.SplitHostPort(d.localAddr())

	branch := strconv.Itoa(int(time.Now().UnixNano() % 1000000))

	d.write("REGISTER sip:" + testServerID + "@" + testRealm + " SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:" + port + ";rport;branch=z9hG4bK" + branch + "\r\n" +
		"From: <sip:" + testDeviceID + "@" + testRealm + ">;tag=abcd1234\r\n" +
		"To: <sip:" + testDeviceID + "@" + testRealm + ">\r\n" +
		"Call-ID: register-call-id-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:" + testDeviceID + "@127.0.0.1:" + port + ">\r\n" +
		"Max-Forwards: 70\r\n" +
		"Expires: 3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	res, err := d.readMessage(3 * time.Second)
	require.NoError(d.t, err)
	require.True(d.t, strings.HasPrefix(res, "SIP/2.0 200"), "unexpected response: %s", res)
}

// replyOK replies to a request with 200 OK, optionally with a body.
func (d *fakeDevice) replyOK(req string, body string) {
	lines := strings.Split(req, "\r\n")

	var via, from, to, callID, cseq string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "Via:"):
			via = line
		case strings.HasPrefix(line, "From:"):
			from = line
		case strings.HasPrefix(line, "To:"):
			to = line
		case strings.HasPrefix(line, "Call-ID:"):
			callID = line
		case strings.HasPrefix(line, "CSeq:"):
			cseq = line
		}
	}

	// the To header of a response must contain a tag.
	if !strings.Contains(to, "tag=") {
		to += ";tag=devicetag01"
	}

	res := "SIP/2.0 200 OK\r\n" +
		via + "\r\n" +
		from + "\r\n" +
		to + "\r\n" +
		callID + "\r\n" +
		cseq + "\r\n" +
		"Content-Length: " + strconv.Itoa(len(body)) + "\r\n"

	if body != "" {
		res += "Content-Type: APPLICATION/SDP\r\n"
	}

	res += "\r\n" + body

	d.write(res)
}

// sendCatalog replies to a catalog query with a channel list.
func (d *fakeDevice) sendCatalog(inviteCSeq int) {
	_, port, _ := net.SplitHostPort(d.localAddr())

	body := `<?xml version="1.0" encoding="GB2312"?>
<Response>
<CmdType>Catalog</CmdType>
<SN>1</SN>
<DeviceID>` + testDeviceID + `</DeviceID>
<SumNum>1</SumNum>
<DeviceList Num="1">
<Item>
<DeviceID>` + testChannelID + `</DeviceID>
<Name>Test Camera</Name>
<Manufacturer>Test</Manufacturer>
<Model>TestModel</Model>
<Status>ON</Status>
<Info>
<PTZType>1</PTZType>
</Info>
</Item>
</DeviceList>
</Response>
`

	d.write("MESSAGE sip:" + testServerID + "@" + testRealm + " SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:" + port + ";rport;branch=z9hG4bKcat" + strconv.Itoa(inviteCSeq) + "\r\n" +
		"From: <sip:" + testDeviceID + "@" + testRealm + ">;tag=catalogtag\r\n" +
		"To: <sip:" + testServerID + "@" + testRealm + ">\r\n" +
		"Call-ID: catalog-call-id-" + strconv.Itoa(inviteCSeq) + "\r\n" +
		"CSeq: " + strconv.Itoa(inviteCSeq) + " MESSAGE\r\n" +
		"Max-Forwards: 70\r\n" +
		"Content-Type: Application/MANSCDP+xml\r\n" +
		"Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" +
		body)

	// consume the 200 OK sent by the server.
	res, err := d.readMessage(3 * time.Second)
	require.NoError(d.t, err)
	require.True(d.t, strings.HasPrefix(res, "SIP/2.0 200"), "unexpected response: %s", res)
}

func testServer(t *testing.T) *Server {
	s := &Server{
		Address:         testServerAddr,
		Transports:      []string{"udp"},
		Serial:          testServerID,
		Realm:           testRealm,
		SIPIP:           "127.0.0.1",
		MediaListenIP:   "127.0.0.1",
		MediaProtocol:   "udp",
		MediaPortMin:    25000,
		MediaPortMax:    25100,
		KeepalivePeriod: conf.Duration(120 * time.Second),
		PathTemplate:    "gb28181/$CHANNEL",
		ReadTimeout:     conf.Duration(10 * time.Second),
		WriteTimeout:    conf.Duration(3 * time.Second),
		Parent:          test.NilLogger,
	}
	err := s.Initialize()
	require.NoError(t, err)

	return s
}

func TestServerRegisterAndCatalog(t *testing.T) {
	s := testServer(t)
	defer s.Close()

	dev := newFakeDevice(t)
	defer dev.close()

	dev.register()

	// the server queries device info and catalog after registration.
	// reply to both queries.
	for range 2 {
		req, err := dev.readMessage(3 * time.Second)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(req, "MESSAGE"), "unexpected request: %s", req)
		dev.replyOK(req, "")
	}

	dev.sendCatalog(2)

	// wait for the catalog to be processed
	require.Eventually(t, func() bool {
		data, err := s.APIChannelsList()
		return err == nil && len(data.Items) == 1
	}, 3*time.Second, 50*time.Millisecond)

	devices, err := s.APIDevicesList()
	require.NoError(t, err)
	require.Equal(t, 1, len(devices.Items))
	require.Equal(t, testDeviceID, devices.Items[0].ID)
	require.True(t, devices.Items[0].Online)

	channels, err := s.APIChannelsList()
	require.NoError(t, err)
	require.Equal(t, 1, len(channels.Items))
	require.Equal(t, testChannelID, channels.Items[0].ID)
	require.Equal(t, "Test Camera", channels.Items[0].Name)
	require.Equal(t, "gb28181/"+testChannelID, channels.Items[0].Path)
	require.True(t, channels.Items[0].Online)
}

func TestServerPTZ(t *testing.T) {
	s := testServer(t)
	defer s.Close()

	dev := newFakeDevice(t)
	defer dev.close()

	dev.register()

	for range 2 {
		req, err := dev.readMessage(3 * time.Second)
		require.NoError(t, err)
		dev.replyOK(req, "")
	}

	dev.sendCatalog(2)

	require.Eventually(t, func() bool {
		data, err := s.APIChannelsList()
		return err == nil && len(data.Items) == 1
	}, 3*time.Second, 50*time.Millisecond)

	ptzDone := make(chan error)
	go func() {
		ptzDone <- s.APIPTZControl(testChannelID, "A50F010800640021")
	}()

	req, err := dev.readMessage(3 * time.Second)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(req, "MESSAGE"), "unexpected request: %s", req)
	require.Contains(t, req, "<CmdType>DeviceControl</CmdType>")
	require.Contains(t, req, "<PTZCmd>A50F010800640021</PTZCmd>")
	require.Contains(t, req, "<DeviceID>"+testChannelID+"</DeviceID>")

	dev.replyOK(req, "")

	require.NoError(t, <-ptzDone)
}

func TestServerPTZChannelNotFound(t *testing.T) {
	s := testServer(t)
	defer s.Close()

	err := s.APIPTZControl("34020000001320009999", "A50F010800640021")
	require.Error(t, err)
}

// TestServerInviteAndMedia performs a complete session: the server sends an INVITE,
// the device replies and sends a MPEG-PS stream over RTP.
func TestServerInviteAndMedia(t *testing.T) {
	s := testServer(t)
	defer s.Close()

	dev := newFakeDevice(t)
	defer dev.close()

	dev.register()

	for range 2 {
		req, err := dev.readMessage(3 * time.Second)
		require.NoError(t, err)
		dev.replyOK(req, "")
	}

	dev.sendCatalog(2)

	require.Eventually(t, func() bool {
		data, err := s.APIChannelsList()
		return err == nil && len(data.Items) == 1
	}, 3*time.Second, 50*time.Millisecond)

	sessDone := make(chan *Session)
	sessErr := make(chan error)

	go func() {
		sess, err := s.StartSession(context.Background(), testChannelID)
		if err != nil {
			sessErr <- err
			return
		}
		sessDone <- sess
	}()

	// receive the INVITE
	invite, err := dev.readMessage(5 * time.Second)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(invite, "INVITE"), "unexpected request: %s", invite)
	require.Contains(t, invite, "m=video ")
	require.Contains(t, invite, "a=rtpmap:96 PS/90000")
	require.Contains(t, invite, "a=recvonly")
	require.Contains(t, invite, "s=Play")
	require.Contains(t, invite, "y=")

	// extract the media port from the SDP
	var mediaPort int
	for line := range strings.SplitSeq(invite, "\r\n") {
		if strings.HasPrefix(line, "m=video ") {
			fields := strings.Fields(line)
			mediaPort, err = strconv.Atoi(fields[1])
			require.NoError(t, err)
		}
	}
	require.NotZero(t, mediaPort)

	// reply with a SDP
	dev.replyOK(invite, "v=0\r\n"+
		"o="+testChannelID+" 0 0 IN IP4 127.0.0.1\r\n"+
		"s=Play\r\n"+
		"c=IN IP4 127.0.0.1\r\n"+
		"t=0 0\r\n"+
		"m=video 6000 RTP/AVP 96\r\n"+
		"a=sendonly\r\n"+
		"a=rtpmap:96 PS/90000\r\n"+
		"y=0100000001\r\n")

	// the ACK must arrive
	ack, err := dev.readMessage(5 * time.Second)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(ack, "ACK"), "unexpected request: %s", ack)

	var sess *Session
	select {
	case sess = <-sessDone:
	case err = <-sessErr:
		t.Fatalf("unable to start session: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out while waiting for the session")
	}
	defer sess.Close()

	// send a MPEG-PS stream over RTP
	mediaConn, err := net.Dial("udp", "127.0.0.1:"+strconv.Itoa(mediaPort))
	require.NoError(t, err)
	defer mediaConn.Close()

	go func() {
		seq := uint16(0)

		for i := range 100 {
			ps := make([]byte, 0, 256)
			ps = append(ps, testPackHeader()...)
			ps = append(ps, testProgramStreamMap()...)
			ps = append(ps, testPESPacket(0xE0, int64(90000+3600*i),
				[]byte{0x00, 0x00, 0x01, 0x67, 0x42, 0xc0, 0x28, 0x01, 0x02, 0x00, 0x00, 0x01, 0x65, 0x03})...)

			pkt := &rtp.Packet{
				Header: rtp.Header{
					Version:        2,
					PayloadType:    96,
					SequenceNumber: seq,
					Timestamp:      uint32(90000 + 3600*i), //nolint:gosec
					SSRC:           100000001,
				},
				Payload: ps,
			}
			seq++

			buf, err2 := pkt.Marshal()
			if err2 != nil {
				return
			}

			mediaConn.Write(buf) //nolint:errcheck
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// the program stream must be readable
	r := &mpegps.Reader{R: sess.Reader()}
	err = r.Initialize()
	require.NoError(t, err)

	tracks := r.Tracks()
	require.Equal(t, 1, len(tracks))
	require.Equal(t, byte(mpegps.StreamTypeH264), tracks[0].StreamType)

	received := make(chan []byte, 16)
	r.OnData(tracks[0], func(_ int64, _ int64, au []byte) error {
		select {
		case received <- au:
		default:
		}
		return nil
	})

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			err2 := r.Read()
			if err2 != nil {
				return
			}
		}
	}()

	select {
	case au := <-received:
		require.Equal(t, []byte{
			0x00, 0x00, 0x01, 0x67, 0x42, 0xc0, 0x28, 0x01, 0x02,
			0x00, 0x00, 0x01, 0x65, 0x03,
		}, au)

	case <-time.After(5 * time.Second):
		t.Fatal("timed out while waiting for media")
	}

	// closing the session must send a BYE
	go sess.Close()

	bye, err := dev.readMessage(5 * time.Second)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(bye, "BYE"), "unexpected request: %s", bye)
	dev.replyOK(bye, "")

	<-readDone
}

func TestServerAuthentication(t *testing.T) {
	s := testServer(t)
	s.Close()

	s = &Server{
		Address:         testServerAddr,
		Transports:      []string{"udp"},
		Serial:          testServerID,
		Realm:           testRealm,
		Password:        "secret",
		SIPIP:           "127.0.0.1",
		MediaListenIP:   "127.0.0.1",
		MediaProtocol:   "udp",
		MediaPortMin:    25000,
		MediaPortMax:    25100,
		KeepalivePeriod: conf.Duration(120 * time.Second),
		PathTemplate:    "gb28181/$CHANNEL",
		ReadTimeout:     conf.Duration(10 * time.Second),
		WriteTimeout:    conf.Duration(3 * time.Second),
		Parent:          test.NilLogger,
	}
	err := s.Initialize()
	require.NoError(t, err)
	defer s.Close()

	dev := newFakeDevice(t)
	defer dev.close()

	_, port, _ := net.SplitHostPort(dev.localAddr())

	dev.write("REGISTER sip:" + testServerID + "@" + testRealm + " SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:" + port + ";rport;branch=z9hG4bKauth1\r\n" +
		"From: <sip:" + testDeviceID + "@" + testRealm + ">;tag=authtag\r\n" +
		"To: <sip:" + testDeviceID + "@" + testRealm + ">\r\n" +
		"Call-ID: auth-call-id\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:" + testDeviceID + "@127.0.0.1:" + port + ">\r\n" +
		"Max-Forwards: 70\r\n" +
		"Expires: 3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	res, err := dev.readMessage(3 * time.Second)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(res, "SIP/2.0 401"), "unexpected response: %s", res)
	require.Contains(t, res, "WWW-Authenticate")
	require.Contains(t, res, "realm=\""+testRealm+"\"")
}

// test helpers that build a MPEG-PS stream.

func testPackHeader() []byte {
	return []byte{
		0x00, 0x00, 0x01, 0xBA,
		0x44, 0x00, 0x04, 0x00, 0x04, 0x01,
		0x00, 0x89, 0xC3, 0xF8,
	}
}

func testProgramStreamMap() []byte {
	esMap := []byte{mpegps.StreamTypeH264, 0xE0, 0x00, 0x00}

	body := make([]byte, 0, 10+len(esMap))
	body = append(body, 0xE0, 0xFF, 0x00, 0x00)
	body = append(body, byte(len(esMap)>>8), byte(len(esMap)))
	body = append(body, esMap...)
	body = append(body, 0x00, 0x00, 0x00, 0x00)

	buf := []byte{0x00, 0x00, 0x01, 0xBC}
	buf = append(buf, byte(len(body)>>8), byte(len(body)))
	return append(buf, body...)
}

func testPESPacket(streamID byte, pts int64, payload []byte) []byte {
	optional := []byte{
		0x02<<4 | byte((pts>>29)&0x0E) | 0x01,
		byte(pts >> 22),
		byte((pts>>14)&0xFE) | 0x01,
		byte(pts >> 7),
		byte((pts<<1)&0xFE) | 0x01,
	}

	body := make([]byte, 0, 3+len(optional)+len(payload))
	body = append(body, 0x80, 0x80, byte(len(optional)))
	body = append(body, optional...)
	body = append(body, payload...)

	buf := []byte{0x00, 0x00, 0x01, streamID}
	buf = append(buf, byte(len(body)>>8), byte(len(body)))
	return append(buf, body...)
}

func TestPathName(t *testing.T) {
	require.Equal(t, "gb28181/123", pathName("gb28181/$CHANNEL", "123"))
	require.Equal(t, "cams/123/live", pathName("cams/$CHANNEL/live", "123"))
}

func TestPortAllocator(t *testing.T) {
	a := &portAllocator{min: 100, max: 102}
	a.initialize()

	ports := make(map[int]struct{})
	for range 3 {
		p, err := a.allocate()
		require.NoError(t, err)
		ports[p] = struct{}{}
	}
	require.Equal(t, 3, len(ports))

	_, err := a.allocate()
	require.Error(t, err)

	a.release(101)
	p, err := a.allocate()
	require.NoError(t, err)
	require.Equal(t, 101, p)
}

func TestGenerateSSRC(t *testing.T) {
	s := &Server{Serial: testServerID}
	ssrc := s.generateSSRC()
	require.Equal(t, 10, len(ssrc))
	require.Equal(t, "0", ssrc[:1])

	_, err := strconv.ParseUint(ssrc, 10, 64)
	require.NoError(t, err)
}

func TestBuildSDP(t *testing.T) {
	sdp := string(buildSDP("34020000001320000001", "192.168.1.1", "192.168.1.2", 20000, mediaProtocolUDP, "0100000001"))
	require.Contains(t, sdp, "m=video 20000 RTP/AVP 96")
	require.Contains(t, sdp, "c=IN IP4 192.168.1.2")
	require.Contains(t, sdp, "y=0100000001")
	require.NotContains(t, sdp, "a=setup:passive")

	sdp = string(buildSDP("34020000001320000001", "192.168.1.1", "192.168.1.2", 20000, mediaProtocolTCP, "0100000001"))
	require.Contains(t, sdp, "m=video 20000 TCP/RTP/AVP 96")
	require.Contains(t, sdp, "a=setup:passive")
}

func TestDefaultIP(t *testing.T) {
	ip, err := defaultIP()
	if err != nil {
		t.Skip("no local IP address available")
	}
	require.NotEmpty(t, ip)
	require.NotNil(t, net.ParseIP(ip))
}
