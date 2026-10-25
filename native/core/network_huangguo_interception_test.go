package core

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

func TestHuangguoBrowserRecoversInterceptedProxyResponse(t *testing.T) {
	for _, recoverable := range []bool{true, false} {
		t.Run(map[bool]string{true: "valid-alternative", false: "all-intercepted"}[recoverable], func(t *testing.T) {
			doh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"Status":0,"Answer":[{"type":1,"TTL":60,"data":"93.184.216.34"}]}`)
			}))
			defer doh.Close()
			d := sourceFixtureDownloader(t, nil)
			transport := newHuangguoBrowserTransport(http.DefaultTransport, d)
			transport.resolvers = []*dnsResolver{{client: doh.Client(), endpoints: []string{doh.URL}, cache: map[string]dnsCacheEntry{}, inFlight: map[string]chan struct{}{}}}
			var hits atomic.Int32
			transport.newClient = func(proxy, host, address string) (browserHTTPClient, error) {
				if proxy != "http://127.0.0.1:7890" || host != "huangguo.video" || transport.insecure {
					t.Error("fallback changed proxy, origin or certificate verification")
				}
				return browserFixtureClient{do: func(r *fhttp.Request) (*fhttp.Response, error) {
					hits.Add(1)
					time.Sleep(time.Millisecond)
					if address != "" && recoverable {
						return &fhttp.Response{StatusCode: 200, Header: make(fhttp.Header), Body: io.NopCloser(strings.NewReader("synthetic-page")), Request: r}, nil
					}
					return &fhttp.Response{StatusCode: 404, ContentLength: 0, Header: make(fhttp.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
				}}, nil
			}
			request, _ := http.NewRequest(http.MethodGet, "https://huangguo.video/series/test", nil)
			response, err := transport.roundTripRoutes(request, "http://127.0.0.1:7890", "fixture")
			if !recoverable {
				var intercepted *proxyInterceptionError
				if !errors.As(err, &intercepted) {
					t.Fatalf("interception became real catalog 404: %v", err)
				}
			} else {
				if err != nil || response.StatusCode != 200 {
					t.Fatalf("fallback failed: %v", err)
				}
				response.Body.Close()
			}
			if hits.Load() != 2 {
				t.Fatalf("unexpected attempt count: %d", hits.Load())
			}
		})
	}
}

func TestHuangguoBrowserPreservesRealHTTPDenials(t *testing.T) {
	for _, status := range []int{403, 404, 429} {
		d := sourceFixtureDownloader(t, nil)
		transport := newHuangguoBrowserTransport(http.DefaultTransport, d)
		transport.fallbackDelay = time.Hour
		var hits atomic.Int32
		transport.newClient = func(string, string, string) (browserHTTPClient, error) {
			return browserFixtureClient{do: func(r *fhttp.Request) (*fhttp.Response, error) {
				hits.Add(1)
				return &fhttp.Response{StatusCode: status, ContentLength: 6, Header: fhttp.Header{"Server": {"source-server"}}, Body: io.NopCloser(strings.NewReader("denied")), Request: r}, nil
			}}, nil
		}
		request, _ := http.NewRequest(http.MethodGet, "https://huangguo.video/series/test", nil)
		response, err := transport.roundTripRoutes(request, "http://127.0.0.1:7890", "fixture")
		if err != nil || response.StatusCode != status || hits.Load() != 1 {
			t.Fatalf("real denial was retried or lost: %d %v", status, err)
		}
		response.Body.Close()
	}
}
