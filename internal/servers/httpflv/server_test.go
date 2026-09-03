package httpflv

import (
	"bufio"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/externalcmd"
	"github.com/bluenviron/mediamtx/internal/stream"
	"github.com/bluenviron/mediamtx/internal/test"
	"github.com/bluenviron/mediamtx/internal/unit"
)

type dummyPathManager struct {
	addReaderImpl func(req defs.PathAddReaderReq) (*defs.PathAddReaderRes, error)
}

func (pm *dummyPathManager) AddReader(req defs.PathAddReaderReq) (*defs.PathAddReaderRes, error) {
	return pm.addReaderImpl(req)
}

type dummyPath struct{}

func (pa *dummyPath) Name() string                                  { return "teststream" }
func (pa *dummyPath) SafeConf() *conf.Path                          { return &conf.Path{} }
func (pa *dummyPath) ExternalCmdEnv() externalcmd.Environment       { return nil }
func (pa *dummyPath) RemovePublisher(_ defs.PathRemovePublisherReq) {}
func (pa *dummyPath) RemoveReader(_ defs.PathRemoveReaderReq)       {}

func TestServerNoOnePublishing(t *testing.T) {
	pathManager := &dummyPathManager{
		addReaderImpl: func(_ defs.PathAddReaderReq) (*defs.PathAddReaderRes, error) {
			return nil, &defs.PathNoStreamAvailableError{PathName: "teststream"}
		},
	}

	s := &Server{
		Address:         "127.0.0.1:9601",
		Encryption:      false,
		AllowOrigins:    []string{"*"},
		ReadTimeout:     conf.Duration(10 * time.Second),
		WriteTimeout:    conf.Duration(10 * time.Second),
		ExternalCmdPool: &externalcmd.Pool{},
		PathManager:     pathManager,
		Parent:          test.NilLogger,
	}
	err := s.Initialize()
	require.NoError(t, err)
	defer s.Close()

	res, err := http.Get("http://127.0.0.1:9601/teststream.flv")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestServerRead(t *testing.T) {
	medias := []*description.Media{{
		Formats: []format.Format{test.FormatH264},
	}}

	strm := &stream.Stream{
		OrigDesc:          &description.Session{Medias: medias},
		WriteQueueSize:    512,
		RTPMaxPayloadSize: 1450,
		Parent:            test.NilLogger,
	}
	err := strm.Initialize()
	require.NoError(t, err)

	subStream := &stream.SubStream{Stream: strm}
	err = subStream.Initialize()
	require.NoError(t, err)

	pa := &dummyPath{}

	pathManager := &dummyPathManager{
		addReaderImpl: func(_ defs.PathAddReaderReq) (*defs.PathAddReaderRes, error) {
			return &defs.PathAddReaderRes{Path: pa, Stream: strm}, nil
		},
	}

	s := &Server{
		Address:         "127.0.0.1:9602",
		Encryption:      false,
		AllowOrigins:    []string{"*"},
		ReadTimeout:     conf.Duration(10 * time.Second),
		WriteTimeout:    conf.Duration(10 * time.Second),
		ExternalCmdPool: &externalcmd.Pool{},
		PathManager:     pathManager,
		Parent:          test.NilLogger,
	}
	err = s.Initialize()
	require.NoError(t, err)
	defer s.Close()

	res, err := http.Get("http://127.0.0.1:9602/teststream.flv")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "video/x-flv", res.Header.Get("Content-Type"))

	br := bufio.NewReader(res.Body)

	header := make([]byte, 13)
	_, err = io.ReadFull(br, header)
	require.NoError(t, err)
	require.Equal(t, []byte("FLV"), header[:3])

	// a non-IDR frame before any IDR must not produce a tag
	subStream.WriteUnit(medias[0], medias[0].Formats[0], &unit.Unit{
		PTS:     0,
		Payload: unit.PayloadH264{{0x61, 0x00}},
	})

	subStream.WriteUnit(medias[0], medias[0].Formats[0], &unit.Unit{
		PTS:     90000,
		Payload: unit.PayloadH264{{0x65, 0x00}},
	})

	tagHeader := make([]byte, 11)
	_, err = io.ReadFull(br, tagHeader)
	require.NoError(t, err)
	require.Equal(t, byte(9), tagHeader[0]) // video tag
}
