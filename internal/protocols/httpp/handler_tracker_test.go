package httpp_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/protocols/httpp"
	"github.com/bluenviron/mediamtx/internal/test"
)

func TestHandlerTracker(t *testing.T) {
	requestReceived := make(chan struct{})

	s := &httpp.Server{
		Address:      "localhost:4667",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		Parent:       test.NilLogger,
		Handler: http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			close(requestReceived)
			time.Sleep(1 * time.Second)
		}),
	}
	err := s.Initialize()
	require.NoError(t, err)

	go func() {
		tr := &http.Transport{}
		defer tr.CloseIdleConnections()
		hc := &http.Client{Transport: tr}

		res, err2 := hc.Get("http://localhost:4667/test")
		require.NoError(t, err2)
		defer res.Body.Close()
	}()

	<-requestReceived

	beforeClose := time.Now()

	s.Close()

	require.Greater(t, time.Since(beforeClose), 1*time.Second)
}

func TestCloseCancelsStreamingHandler(t *testing.T) {
	received := make(chan struct{})
	canceled := make(chan struct{})
	s := &httpp.Server{Address: "localhost:4668", ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, Parent: test.NilLogger, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(received)
		<-r.Context().Done()
		close(canceled)
	})}
	require.NoError(t, s.Initialize())
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Get("http://localhost:4668/stream")
	require.NoError(t, err)
	defer res.Body.Close()
	<-received
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("server close did not interrupt active stream")
	}
	select {
	case <-canceled:
	default:
		t.Fatal("request context was not canceled")
	}
}
