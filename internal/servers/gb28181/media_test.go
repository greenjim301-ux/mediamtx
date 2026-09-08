package gb28181

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/require"
)

func TestReorderer(t *testing.T) {
	r := &reorderer{}
	r.initialize()

	// in order
	require.Equal(t, [][]byte{{1}}, r.process(10, []byte{1}))
	require.Equal(t, [][]byte{{2}}, r.process(11, []byte{2}))

	// out of order: 13 arrives before 12
	require.Nil(t, r.process(13, []byte{4}))
	require.Equal(t, [][]byte{{3}, {4}}, r.process(12, []byte{3}))

	// late packet is discarded
	require.Nil(t, r.process(11, []byte{99}))

	// sequence number wraparound
	r2 := &reorderer{}
	r2.initialize()
	require.Equal(t, [][]byte{{1}}, r2.process(65535, []byte{1}))
	require.Equal(t, [][]byte{{2}}, r2.process(0, []byte{2}))
	require.Equal(t, [][]byte{{3}}, r2.process(1, []byte{3}))
}

func TestReordererGiveUpOnLostPackets(t *testing.T) {
	r := &reorderer{}
	r.initialize()

	require.Equal(t, [][]byte{{0}}, r.process(0, []byte{0}))

	// packet 1 is lost forever; the following ones are buffered
	// until the buffer is full, then they are emitted anyway.
	var out [][]byte
	for i := 2; i < 2+reorderBufferSize; i++ {
		out = append(out, r.process(uint16(i), []byte{byte(i)})...) //nolint:gosec
	}

	require.NotEmpty(t, out)
	require.Equal(t, []byte{2}, out[0])
}

func TestPayloadPipe(t *testing.T) {
	p := newPayloadPipe()

	p.Write([]byte{1, 2, 3})
	p.Write([]byte{4, 5})

	buf := make([]byte, 3)
	n, err := p.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.Equal(t, []byte{1, 2, 3}, buf[:n])

	n, err = p.Read(buf)
	require.NoError(t, err)
	require.Equal(t, []byte{4, 5}, buf[:n])

	// a blocked reader is unblocked by a write
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf2 := make([]byte, 8)
		n2, err2 := p.Read(buf2)
		require.NoError(t, err2)
		require.Equal(t, []byte{6}, buf2[:n2])
	}()

	time.Sleep(100 * time.Millisecond)
	p.Write([]byte{6})
	<-done

	// after closing, reads return the error
	p.closeWithError(io.EOF)
	_, err = p.Read(buf)
	require.Equal(t, io.EOF, err)
}

func TestPayloadPipeOverflow(t *testing.T) {
	p := newPayloadPipe()

	big := make([]byte, pipeMaxSize/2)
	for range 4 {
		p.Write(big)
	}

	require.NotZero(t, p.droppedCount())
}

func TestMediaReceiverUDP(t *testing.T) {
	m := &mediaReceiver{
		protocol:    mediaProtocolUDP,
		address:     "127.0.0.1:25501",
		readTimeout: 5 * time.Second,
	}
	err := m.initialize()
	require.NoError(t, err)
	defer m.close()

	conn, err := net.Dial("udp", "127.0.0.1:25501")
	require.NoError(t, err)
	defer conn.Close()

	pkt := &rtp.Packet{
		Header:  rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: 5, SSRC: 1},
		Payload: []byte{0x01, 0x02, 0x03, 0x04},
	}
	buf, err := pkt.Marshal()
	require.NoError(t, err)

	_, err = conn.Write(buf)
	require.NoError(t, err)

	out := make([]byte, 4)
	_, err = io.ReadFull(m.reader(), out)
	require.NoError(t, err)
	require.Equal(t, []byte{0x01, 0x02, 0x03, 0x04}, out)
}

func TestMediaReceiverTCP(t *testing.T) {
	m := &mediaReceiver{
		protocol:    mediaProtocolTCP,
		address:     "127.0.0.1:25502",
		readTimeout: 5 * time.Second,
	}
	err := m.initialize()
	require.NoError(t, err)
	defer m.close()

	conn, err := net.Dial("tcp", "127.0.0.1:25502")
	require.NoError(t, err)
	defer conn.Close()

	for i := range 2 {
		pkt := &rtp.Packet{
			Header:  rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: uint16(i), SSRC: 1}, //nolint:gosec
			Payload: []byte{byte(i), 0x02},
		}
		buf, err2 := pkt.Marshal()
		require.NoError(t, err2)

		// RTP over TCP is framed with a 2-byte length (RFC 4571).
		frame := make([]byte, 2+len(buf))
		binary.BigEndian.PutUint16(frame, uint16(len(buf))) //nolint:gosec
		copy(frame[2:], buf)

		_, err2 = conn.Write(frame)
		require.NoError(t, err2)
	}

	out := make([]byte, 4)
	_, err = io.ReadFull(m.reader(), out)
	require.NoError(t, err)
	require.Equal(t, []byte{0x00, 0x02, 0x01, 0x02}, out)
}
