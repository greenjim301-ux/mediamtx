package gb28181

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/pion/rtp"
)

const (
	// maximum size of the buffer that decouples the network from the demuxer.
	pipeMaxSize = 2 * 1024 * 1024

	// maximum number of out-of-order RTP packets kept while waiting for a missing one.
	reorderBufferSize = 64

	udpReadBufferSize = 2048
)

// payloadPipe is a buffer that decouples the goroutine that receives
// RTP packets from the one that demuxes the program stream.
type payloadPipe struct {
	mutex   sync.Mutex
	cond    *sync.Cond
	buf     []byte
	err     error
	dropped uint64
}

func newPayloadPipe() *payloadPipe {
	p := &payloadPipe{}
	p.cond = sync.NewCond(&p.mutex)
	return p
}

func (p *payloadPipe) Write(buf []byte) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.err != nil {
		return
	}

	if (len(p.buf) + len(buf)) > pipeMaxSize {
		// the demuxer is not keeping up: discard the oldest data.
		p.buf = nil
		p.dropped++
	}

	p.buf = append(p.buf, buf...)
	p.cond.Broadcast()
}

// Read implements io.Reader.
func (p *payloadPipe) Read(buf []byte) (int, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	for len(p.buf) == 0 {
		if p.err != nil {
			return 0, p.err
		}
		p.cond.Wait()
	}

	n := copy(buf, p.buf)
	p.buf = p.buf[n:]

	return n, nil
}

func (p *payloadPipe) closeWithError(err error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.err == nil {
		p.err = err
	}
	p.cond.Broadcast()
}

func (p *payloadPipe) droppedCount() uint64 {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.dropped
}

// reorderer puts RTP packets in order, in order to compensate
// the packet reordering of UDP networks.
type reorderer struct {
	initialized bool
	expected    uint16
	buffered    map[uint16][]byte
}

func (r *reorderer) initialize() {
	r.buffered = make(map[uint16][]byte)
}

// process returns the payloads that are ready to be used, in order.
func (r *reorderer) process(seq uint16, payload []byte) [][]byte {
	if !r.initialized {
		r.initialized = true
		r.expected = seq
	}

	diff := int16(seq - r.expected) //nolint:gosec

	switch {
	case diff < 0:
		// packet is late: it has already been processed or skipped.
		return nil

	case diff > 0:
		r.buffered[seq] = payload

		if len(r.buffered) < reorderBufferSize {
			return nil
		}

		// too many pending packets: give up on the missing ones
		// and restart from the oldest available.
		oldest := seq
		for s := range r.buffered {
			if int16(s-oldest) < 0 { //nolint:gosec
				oldest = s
			}
		}
		r.expected = oldest
	}

	var out [][]byte

	for {
		buf, ok := r.buffered[r.expected]
		if !ok {
			if r.expected == seq {
				out = append(out, payload)
				delete(r.buffered, r.expected)
				r.expected++
				continue
			}
			break
		}

		out = append(out, buf)
		delete(r.buffered, r.expected)
		r.expected++
	}

	return out
}

// mediaProtocol is the transport protocol of a media stream.
type mediaProtocol string

// media protocols.
const (
	mediaProtocolUDP mediaProtocol = "udp"
	mediaProtocolTCP mediaProtocol = "tcp"
)

// mediaReceiver receives a RTP/PS stream sent by a device.
type mediaReceiver struct {
	protocol    mediaProtocol
	address     string
	readTimeout time.Duration

	pc   net.PacketConn
	ln   net.Listener
	pipe *payloadPipe
	done chan struct{}

	mutex sync.Mutex
	conn  net.Conn
}

func (m *mediaReceiver) setConn(conn net.Conn) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.conn = conn
}

func (m *mediaReceiver) closeConn() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.conn != nil {
		m.conn.Close()
	}
}

func (m *mediaReceiver) initialize() error {
	m.pipe = newPayloadPipe()
	m.done = make(chan struct{})

	if m.protocol == mediaProtocolTCP {
		ln, err := net.Listen("tcp", m.address)
		if err != nil {
			return err
		}
		m.ln = ln
		go m.runTCP()
		return nil
	}

	pc, err := net.ListenPacket("udp", m.address)
	if err != nil {
		return err
	}
	m.pc = pc
	go m.runUDP()

	return nil
}

func (m *mediaReceiver) close() {
	if m.pc != nil {
		m.pc.Close()
	}
	if m.ln != nil {
		m.ln.Close()
	}
	m.closeConn()
	m.pipe.closeWithError(fmt.Errorf("terminated"))
	<-m.done
}

// reader returns the program stream sent by the device.
func (m *mediaReceiver) reader() io.Reader {
	return m.pipe
}

func (m *mediaReceiver) runUDP() {
	defer close(m.done)

	buf := make([]byte, udpReadBufferSize)
	reord := &reorderer{}
	reord.initialize()

	for {
		m.pc.SetReadDeadline(time.Now().Add(m.readTimeout)) //nolint:errcheck

		n, _, err := m.pc.ReadFrom(buf)
		if err != nil {
			m.pipe.closeWithError(err)
			return
		}

		var pkt rtp.Packet
		err = pkt.Unmarshal(buf[:n])
		if err != nil {
			continue
		}

		for _, payload := range reord.process(pkt.SequenceNumber, append([]byte(nil), pkt.Payload...)) {
			m.pipe.Write(payload)
		}
	}
}

func (m *mediaReceiver) runTCP() {
	defer close(m.done)

	conn, err := m.ln.Accept()
	if err != nil {
		m.pipe.closeWithError(err)
		return
	}
	defer conn.Close()
	m.setConn(conn)

	// stop accepting other connections.
	m.ln.Close()

	header := make([]byte, 2)

	for {
		conn.SetReadDeadline(time.Now().Add(m.readTimeout)) //nolint:errcheck

		// RTP over TCP is framed with a 2-byte length, as defined by RFC 4571.
		_, err = io.ReadFull(conn, header)
		if err != nil {
			m.pipe.closeWithError(err)
			return
		}

		le := binary.BigEndian.Uint16(header)
		if le == 0 {
			continue
		}

		buf := make([]byte, le)

		_, err = io.ReadFull(conn, buf)
		if err != nil {
			m.pipe.closeWithError(err)
			return
		}

		var pkt rtp.Packet
		err = pkt.Unmarshal(buf)
		if err != nil {
			continue
		}

		m.pipe.Write(pkt.Payload)
	}
}
