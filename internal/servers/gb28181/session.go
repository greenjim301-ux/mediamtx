package gb28181

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emiago/sipgo/sip"

	"github.com/bluenviron/mediamtx/internal/logger"
)

// Session is a media session with a device, established through INVITE.
type Session struct {
	server    *Server
	channelID string
	deviceID  string

	port      int
	receiver  *mediaReceiver
	invite    *sip.Request
	inviteRes *sip.Response
	callID    string

	closeOnce sync.Once
}

// ChannelID returns the ID of the channel of the session.
func (s *Session) ChannelID() string {
	return s.channelID
}

// Reader returns the MPEG-PS stream sent by the device.
func (s *Session) Reader() io.Reader {
	return s.receiver.reader()
}

// Close closes the session, sending a BYE to the device.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		s.server.removeSession(s)

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(s.server.WriteTimeout))
		defer cancel()

		err := s.server.sendBye(ctx, s)
		if err != nil {
			s.server.Log(logger.Debug, "unable to send BYE for channel '%s': %v", s.channelID, err)
		}

		s.receiver.close()
		s.server.ports.release(s.port)

		if dropped := s.receiver.pipe.droppedCount(); dropped != 0 {
			s.server.Log(logger.Warn, "channel '%s': %d media buffer overflows", s.channelID, dropped)
		}
	})
}

// buildSDP builds the SDP of an INVITE request.
func buildSDP(
	channelID string,
	sipIP string,
	mediaIP string,
	mediaPort int,
	protocol mediaProtocol,
	ssrc string,
) []byte {
	var lines []string

	lines = append(lines,
		"v=0",
		"o="+channelID+" 0 0 IN IP4 "+sipIP,
		"s=Play",
		"c=IN IP4 "+mediaIP,
		"t=0 0",
	)

	if protocol == mediaProtocolTCP {
		lines = append(lines,
			"m=video "+strconv.Itoa(mediaPort)+" TCP/RTP/AVP 96",
			"a=recvonly",
			"a=rtpmap:96 PS/90000",
			"a=setup:passive",
			"a=connection:new",
		)
	} else {
		lines = append(lines,
			"m=video "+strconv.Itoa(mediaPort)+" RTP/AVP 96",
			"a=recvonly",
			"a=rtpmap:96 PS/90000",
		)
	}

	lines = append(lines, "y="+ssrc, "")

	return []byte(strings.Join(lines, "\r\n"))
}

// StartSession pulls the live stream of a channel, by sending an INVITE to its device.
func (s *Server) StartSession(ctx context.Context, channelID string) (*Session, error) {
	dev, _, err := s.findChannel(channelID)
	if err != nil {
		return nil, err
	}

	addr, transport := dev.target()
	if addr == "" {
		return nil, fmt.Errorf("device '%s' is not registered", dev.id)
	}

	port, err := s.ports.allocate()
	if err != nil {
		return nil, err
	}

	receiver := &mediaReceiver{
		protocol:    s.mediaProtocol(),
		address:     net.JoinHostPort(s.MediaListenIP, strconv.Itoa(port)),
		readTimeout: time.Duration(s.ReadTimeout),
	}

	err = receiver.initialize()
	if err != nil {
		s.ports.release(port)
		return nil, err
	}

	sess := &Session{
		server:    s,
		channelID: channelID,
		deviceID:  dev.id,
		port:      port,
		receiver:  receiver,
	}

	err = s.sendInvite(ctx, sess, addr, transport, port)
	if err != nil {
		receiver.close()
		s.ports.release(port)
		return nil, err
	}

	s.addSession(sess)

	s.Log(logger.Info, "channel '%s' of device '%s' is streaming to port %d (%s)",
		channelID, dev.id, port, s.mediaProtocol())

	return sess, nil
}

func (s *Server) sendInvite(
	ctx context.Context,
	sess *Session,
	addr string,
	transport string,
	port int,
) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	devPort, _ := strconv.Atoi(portStr)

	ssrc := s.generateSSRC()

	recipient := sip.Uri{
		Scheme: "sip",
		User:   sess.channelID,
		Host:   host,
		Port:   devPort,
	}

	req := sip.NewRequest(sip.INVITE, recipient)
	req.SipVersion = "SIP/2.0"

	from := &sip.FromHeader{
		Address: sip.Uri{Scheme: "sip", User: s.Serial, Host: s.Realm},
		Params:  sip.NewParams(),
	}
	from.Params.Add("tag", sip.GenerateTagN(16))
	req.AppendHeader(from)

	req.AppendHeader(&sip.ToHeader{
		Address: sip.Uri{Scheme: "sip", User: sess.channelID, Host: s.Realm},
	})

	req.AppendHeader(&sip.ContactHeader{
		Address: sip.Uri{Scheme: "sip", User: s.Serial, Host: s.SIPIP, Port: s.sipPort},
	})

	req.AppendHeader(sip.NewHeader("Subject",
		fmt.Sprintf("%s:%s,%s:0", sess.channelID, ssrc, s.Serial)))
	req.AppendHeader(sip.NewHeader("Content-Type", "APPLICATION/SDP"))

	req.SetBody(buildSDP(
		sess.channelID,
		s.SIPIP,
		s.mediaAdvertisedIP(),
		port,
		s.mediaProtocol(),
		ssrc,
	))

	req.SetDestination(addr)
	req.SetTransport(strings.ToUpper(transport))

	res, err := s.sipClient.Do(ctx, req)
	if err != nil {
		return err
	}

	if res.StatusCode != sip.StatusOK {
		return fmt.Errorf("device replied with code %d (%s)", res.StatusCode, res.Reason)
	}

	sess.invite = req
	sess.inviteRes = res

	if callID := req.CallID(); callID != nil {
		sess.callID = callID.Value()
	}

	// the ACK of a 2xx response is sent outside of the transaction.
	ack := newACK(req, res)
	ack.SetDestination(addr)
	ack.SetTransport(strings.ToUpper(transport))

	err = s.sipClient.WriteRequest(ack)
	if err != nil {
		return err
	}

	return nil
}

func (s *Server) sendBye(ctx context.Context, sess *Session) error {
	if sess.invite == nil || sess.inviteRes == nil {
		return nil
	}

	dev, err := s.findDevice(sess.deviceID)
	if err != nil {
		return err
	}

	addr, transport := dev.target()
	if addr == "" {
		return fmt.Errorf("device is not registered")
	}

	bye := newBYE(sess.invite, sess.inviteRes)
	bye.SetDestination(addr)
	bye.SetTransport(strings.ToUpper(transport))

	_, err = s.sipClient.Do(ctx, bye)
	return err
}

// newACK builds the ACK of a 2xx response, as defined by RFC 3261, section 13.2.2.4.
func newACK(invite *sip.Request, res *sip.Response) *sip.Request {
	recipient := invite.Recipient
	if contact := res.Contact(); contact != nil {
		recipient = contact.Address
	}

	ack := sip.NewRequest(sip.ACK, recipient)
	ack.SipVersion = invite.SipVersion

	if h := invite.From(); h != nil {
		ack.AppendHeader(sip.HeaderClone(h))
	}
	if h := res.To(); h != nil {
		ack.AppendHeader(sip.HeaderClone(h))
	}
	if h := invite.CallID(); h != nil {
		ack.AppendHeader(sip.HeaderClone(h))
	}
	if h := invite.CSeq(); h != nil {
		ack.AppendHeader(sip.HeaderClone(h))
		ack.CSeq().MethodName = sip.ACK
	}
	if h := invite.Contact(); h != nil {
		ack.AppendHeader(sip.HeaderClone(h))
	}

	maxForwards := sip.MaxForwardsHeader(70)
	ack.AppendHeader(&maxForwards)

	return ack
}

// newBYE builds a BYE request inside an established dialog.
func newBYE(invite *sip.Request, res *sip.Response) *sip.Request {
	recipient := invite.Recipient
	if contact := res.Contact(); contact != nil {
		recipient = contact.Address
	}

	bye := sip.NewRequest(sip.BYE, recipient)
	bye.SipVersion = invite.SipVersion

	if h := invite.From(); h != nil {
		bye.AppendHeader(sip.HeaderClone(h))
	}
	if h := res.To(); h != nil {
		bye.AppendHeader(sip.HeaderClone(h))
	}
	if h := invite.CallID(); h != nil {
		bye.AppendHeader(sip.HeaderClone(h))
	}
	if h := invite.CSeq(); h != nil {
		bye.AppendHeader(sip.HeaderClone(h))
		cseq := bye.CSeq()
		cseq.SeqNo++
		cseq.MethodName = sip.BYE
	}

	maxForwards := sip.MaxForwardsHeader(70)
	bye.AppendHeader(&maxForwards)

	return bye
}
