package core

import (
	"context"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

func TestYeguoAPIKeepsPostBodyAcrossVerifiedDNSRoutes(t *testing.T) {
	doh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"Status":0,"Answer":[{"type":1,"TTL":60,"data":"93.184.216.34"}]}`)
	}))
	defer doh.Close()
	d := sourceFixtureDownloader(t, nil)
	d.proxyRouter = &proxyRouter{mode: "direct"}
	transport := newSourceBrowserTransport(http.DefaultTransport, d, sourceYeguo, "")
	transport.resolvers = []*dnsResolver{{client: doh.Client(), endpoints: []string{doh.URL}, cache: map[string]dnsCacheEntry{}, inFlight: map[string]chan struct{}{}}}
	var calls atomic.Int32
	transport.newClient = func(proxy, host, address string) (browserHTTPClient, error) {
		if host != "www.yeguodj.com" || transport.insecure {
			t.Error("API lost its original TLS identity")
		}
		return browserFixtureClient{do: func(r *fhttp.Request) (*fhttp.Response, error) {
			calls.Add(1)
			body, err := io.ReadAll(r.Body)
			r.Body.Close()
			if err != nil || string(body) != "page=1&limit=20" || r.Method != http.MethodPost || r.URL.Host != host {
				t.Error("read-only API request changed during route fallback")
			}
			if address == "" {
				return nil, x509.UnknownAuthorityError{}
			}
			return &fhttp.Response{StatusCode: 200, Header: make(fhttp.Header), Body: io.NopCloser(strings.NewReader(`{"status":"1"}`)), Request: r}, nil
		}}, nil
	}
	request, _ := http.NewRequest(http.MethodPost, "https://www.yeguodj.com/api.php/api/theater/exploreList", strings.NewReader("page=1&limit=20"))
	response, err := transport.RoundTrip(request)
	if err != nil || calls.Load() != 2 {
		t.Fatal("API fallback failed", err, calls.Load())
	}
	response.Body.Close()
	request, _ = http.NewRequest(http.MethodPost, "https://www.yeguodj.com/api.php/api/account/login", strings.NewReader("synthetic-only"))
	if transport.matches(request) {
		t.Fatal("state-changing API was allowed to replay")
	}
}

func TestSourceBrowserUsesAlternativeBeforeSlowDefaultExhaustsDeadline(t *testing.T) {
	doh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"Status":0,"Answer":[{"type":1,"TTL":60,"data":"93.184.216.34"}]}`)
	}))
	defer doh.Close()
	d := sourceFixtureDownloader(t, nil)
	d.proxyRouter = &proxyRouter{mode: "direct"}
	transport := newHuangguoBrowserTransport(http.DefaultTransport, d)
	transport.resolvers = []*dnsResolver{{client: doh.Client(), endpoints: []string{doh.URL}, cache: map[string]dnsCacheEntry{}, inFlight: map[string]chan struct{}{}}}
	transport.newClient = func(proxy, host, address string) (browserHTTPClient, error) {
		return browserFixtureClient{do: func(request *fhttp.Request) (*fhttp.Response, error) {
			if address == "" {
				<-request.Context().Done()
				return nil, request.Context().Err()
			}
			return &fhttp.Response{StatusCode: 200, Header: make(fhttp.Header), Body: io.NopCloser(strings.NewReader("synthetic-alternative")), Request: request}, nil
		}}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://huangguo.video/segment.ts", nil)
	response, err := transport.RoundTrip(request)
	if err != nil || ctx.Err() != nil {
		t.Fatal("slow default route consumed the complete caller deadline", err)
	}
	response.Body.Close()
}

func TestPublicSourceBrowserKeepsImportedUserAgentScoped(t *testing.T) {
	d := sourceFixtureDownloader(t, nil)
	d.proxyRouter = &proxyRouter{mode: "direct"}
	d.sourceAccess = map[string]nativeSourceAccess{
		sourceDSD: {Origin: "https://www.dsd.com.se", UserAgent: "synthetic-imported-browser"},
	}
	transport := newHuangguoBrowserTransport(http.DefaultTransport, d)
	transport.newClient = func(proxy, host, address string) (browserHTTPClient, error) {
		return browserFixtureClient{do: func(r *fhttp.Request) (*fhttp.Response, error) {
			headers := standardBrowserHeaders(r.Header)
			if r.URL.Host == "www.dsd.com.se" {
				if headers.Get("User-Agent") != "synthetic-imported-browser" || headers.Get("Sec-Ch-Ua") != "" {
					t.Error("imported user agent was replaced")
				}
			} else if headers.Get("User-Agent") == "synthetic-imported-browser" {
				t.Error("imported user agent crossed its origin")
			}
			return &fhttp.Response{StatusCode: 200, Header: make(fhttp.Header), Body: io.NopCloser(strings.NewReader("synthetic-page")), Request: r}, nil
		}}, nil
	}
	for address, source := range map[string]string{
		"https://www.dsd.com.se/":               sourceDSD,
		"https://analyze.buxefaex.cc/":          sourceYeguo,
		"https://cdn.huangguo.video/segment.ts": sourceHuangguoVideo,
	} {
		request, _ := http.NewRequest(http.MethodGet, address, nil)
		if !transport.matches(request) || transport.requestSource(request) != source {
			t.Fatal("incorrect public source routing", address)
		}
		response, err := transport.RoundTrip(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
}
