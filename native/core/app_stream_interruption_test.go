package core

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type truncatedFixtureBody struct{ io.Reader }

func (*truncatedFixtureBody) Close() error { return nil }

type fixtureReadFailure struct{}

func (fixtureReadFailure) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestNativeStreamRecordsTruncatedSuccessfulResponseAsFailure(t *testing.T) {
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		response := sourceFixtureResponse(request, 200, "")
		response.ContentLength = 128
		response.Header.Set("Content-Length", "128")
		response.Header.Set("Content-Type", "video/mp2t")
		response.Body = &truncatedFixtureBody{Reader: io.MultiReader(strings.NewReader(strings.Repeat("synthetic", 4)), fixtureReadFailure{})}
		return response, nil
	})
	stream, err := newNativeStreamServer(d)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.server.Close()
	address, token := stream.nativeOpen(providerMedia{URL: "https://media.example/segment.ts"})
	defer stream.nativeRelease(token)
	response, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr == nil {
		t.Fatal("truncated transfer was reported complete")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		session := stream.sessions[token]
		session.mu.Lock()
		failures := strings.Join(session.failures, "\n")
		session.mu.Unlock()
		if strings.Contains(failures, "转发中断") {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("HTTP 200 body failure was omitted from stream diagnostics")
}
