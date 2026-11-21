package core

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"testing"
)

type testIdleTransport func(*http.Request) (*http.Response, error)

func (transport testIdleTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func (testIdleTransport) CloseIdleConnections() {}

func TestHanxiaoquanWindowsMediaNetwork(t *testing.T) {
	for _, step := range []struct {
		name, mode, ip string
		media          bool
		lanStatus      int
		wantLAN        bool
		wantStatus     int
	}{
		{"auto fake IP", "auto", "198.18.0.4", true, 200, true, 200},
		{"direct fake IP", "direct", "198.19.0.4", true, 200, true, 200},
		{"LAN connect failure", "auto", "198.18.0.4", true, 0, true, 204},
		{"LAN authorization", "auto", "198.18.0.4", true, 403, true, 403},
		{"manual", "manual", "198.18.0.4", true, 200, false, 204},
		{"regular resolution", "direct", "8.8.8.8", true, 200, false, 204},
		{"unrelated request", "auto", "198.18.0.4", false, 200, false, 204},
	} {
		t.Run(step.name, func(t *testing.T) {
			lanCalls, nextCalls := 0, 0
			transport := &hanxiaoquanMediaNetwork{
				router: &proxyRouter{mode: step.mode},
				lookup: func(context.Context, string, string) ([]netip.Addr, error) {
					return []netip.Addr{netip.MustParseAddr(step.ip)}, nil
				},
				lan: testIdleTransport(func(request *http.Request) (*http.Response, error) {
					lanCalls++
					if request.URL.Hostname() != "media.example.test" || request.Header.Get("Range") != "bytes=0-255" {
						t.Fatal("LAN route changed the media request")
					}
					if step.lanStatus == 0 {
						return nil, errors.New("fixture LAN connection failure")
					}
					return sourceFixtureResponse(request, step.lanStatus, ""), nil
				}),
				next: testIdleTransport(func(request *http.Request) (*http.Response, error) {
					nextCalls++
					return sourceFixtureResponse(request, http.StatusNoContent, ""), nil
				}),
			}
			ctx := context.WithValue(context.Background(), hanxiaoquanMediaNetworkKey{}, step.media)
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://media.example.test/segment.ts", nil)
			request.Header.Set("Range", "bytes=0-255")
			response, err := transport.RoundTrip(request)
			if err != nil || response.StatusCode != step.wantStatus || (lanCalls == 1) != step.wantLAN || (nextCalls == 1) != (step.wantStatus == 204) {
				t.Fatalf("response=%v error=%v LAN=%d default=%d", response, err, lanCalls, nextCalls)
			}
			response.Body.Close()
		})
	}
}
