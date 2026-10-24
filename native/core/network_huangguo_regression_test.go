package core

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

type browserFixtureClient struct {
	do func(*fhttp.Request) (*fhttp.Response, error)
}

func (client browserFixtureClient) Do(request *fhttp.Request) (*fhttp.Response, error) {
	return client.do(request)
}

func (client browserFixtureClient) CloseIdleConnections() {}

func TestHuangguoCDNUsesBrowserMediaHeaders(t *testing.T) {
	d := sourceFixtureDownloader(t, nil)
	d.proxyRouter = &proxyRouter{}
	transport := newHuangguoBrowserTransport(sourceFixtureTransport(func(*http.Request) (*http.Response, error) {
		t.Error("CDN media fell back to ordinary TLS transport")
		return nil, errors.New("wrong transport")
	}), d)
	transport.newClient = func(string, string, string) (browserHTTPClient, error) {
		return browserFixtureClient{do: func(request *fhttp.Request) (*fhttp.Response, error) {
			headers := standardBrowserHeaders(request.Header)
			if headers.Get("Sec-Fetch-Site") != "same-site" || headers.Get("Sec-Fetch-Mode") != "cors" || !strings.Contains(headers.Get("User-Agent"), "Chrome/150.") {
				t.Error("CDN lost browser media headers")
			}
			if headers.Get("X-Preview-Token") != "" || headers.Get("Cookie") != "" {
				t.Error("private main-origin header leaked to CDN")
			}
			return &fhttp.Response{StatusCode: 200, Header: make(fhttp.Header), Body: io.NopCloser(strings.NewReader("synthetic-segment")), Request: request}, nil
		}}, nil
	}
	d.client.Transport = transport
	request, _ := http.NewRequest(http.MethodGet, "https://cdn.huangguo.video/segment.ts", nil)
	request.Header.Set("Referer", "https://huangguo.video/video/fixture")
	response, err := d.doMediaRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	for _, host := range []string{"huangguo.video.evil.example", "nothuangguo.video", "other.example"} {
		request, _ := http.NewRequest(http.MethodGet, "https://"+host+"/segment.ts", nil)
		if transport.matches(request) {
			t.Fatal("unrelated host entered source browser session", host)
		}
	}
}

func TestHuangguoMediaRetriesTransientFailureWithoutRepeatingDenial(t *testing.T) {
	for _, status := range []int{503, 403, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
				if calls.Add(1) == 1 {
					return sourceFixtureResponse(request, status, "synthetic-status"), nil
				}
				return sourceFixtureResponse(request, 200, "synthetic-segment"), nil
			})
			request, _ := http.NewRequest(http.MethodGet, "https://cdn.huangguo.video/segment.ts", nil)
			response, err := d.doMediaRequest(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			want := int32(1)
			if status == 503 {
				want = 2
				if response.StatusCode != 200 {
					t.Fatal("transient failure was not recovered")
				}
			}
			if calls.Load() != want {
				t.Fatal("unexpected retry count", calls.Load())
			}
		})
	}
}

func TestHuangguoMediaPreservesCancellationAndTLSFailure(t *testing.T) {
	var calls atomic.Int32
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, x509.UnknownAuthorityError{}
	})
	request, _ := http.NewRequest(http.MethodGet, "https://cdn.huangguo.video/segment.ts", nil)
	if _, err := d.doMediaRequest(request); err == nil || calls.Load() != 1 {
		t.Fatal("certificate error should remain terminal")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request = request.WithContext(ctx)
	if _, err := d.doMediaRequest(request); !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatal("cancelled media request was retried", err)
	}
}

func TestHuangguoMediaBodySurvivesHeaderDeadline(t *testing.T) {
	d := sourceFixtureDownloader(t, nil)
	d.proxyRouter = &proxyRouter{mode: "direct"}
	transport := newHuangguoBrowserTransport(http.DefaultTransport, d)
	transport.newClient = func(string, string, string) (browserHTTPClient, error) {
		return browserFixtureClient{do: func(request *fhttp.Request) (*fhttp.Response, error) {
			return &fhttp.Response{StatusCode: 200, Header: make(fhttp.Header), Body: &contextFixtureBody{ctx: request.Context()}, Request: request}, nil
		}}, nil
	}
	d.client.Transport = transport
	request, _ := http.NewRequest(http.MethodGet, "https://cdn.huangguo.video/segment.ts", nil)
	response, err := d.doMediaRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	time.Sleep(3100 * time.Millisecond)
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal("header timeout cancelled a healthy response body", err)
	}
}

type contextFixtureBody struct{ ctx context.Context }

func (body *contextFixtureBody) Read([]byte) (int, error) {
	if err := body.ctx.Err(); err != nil {
		return 0, err
	}
	return 0, io.EOF
}

func (*contextFixtureBody) Close() error { return nil }

func TestHuangguoBrowserSeparatesProxyConnectAndResponseTimeout(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "huangguo.video" || r.TLS.ServerName != "huangguo.video" {
			t.Error("pinned proxy route lost original HTTP Host or TLS SNI")
		}
		io.WriteString(w, "synthetic-proxy-media")
	}))
	defer origin.Close()
	var connects atomic.Int32
	var expectedTarget atomic.Value
	expectedTarget.Store("huangguo.video:443")
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != expectedTarget.Load().(string) {
			t.Error("unexpected proxy request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		connects.Add(1)
		upstream, err := net.DialTimeout("tcp", strings.TrimPrefix(origin.URL, "https://"), time.Second)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer client.Close()
		buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		buffered.Flush()
		go func() { _, _ = io.Copy(upstream, buffered) }()
		_, _ = io.Copy(client, upstream)
	}))
	defer proxy.Close()
	d := sourceFixtureDownloader(t, nil)
	d.cfg.HuangguoVideoURL, d.cfg.InsecureTLS = origin.URL, true
	proxyURL, _ := url.Parse(proxy.URL)
	d.proxyRouter = &proxyRouter{mode: "manual", manual: proxyURL}
	transport := newHuangguoBrowserTransport(http.DefaultTransport, d)
	defer transport.CloseIdleConnections()
	d.client.Transport = transport
	request, _ := http.NewRequest(http.MethodGet, "https://huangguo.video/segment.ts", nil)
	response, err := d.doMediaRequest(request)
	if err != nil {
		t.Fatal("proxy handshake failed with streaming timeout disabled", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "synthetic-proxy-media" || connects.Load() < 1 {
		t.Fatalf("proxy media response failed: status=%d body=%q connects=%d err=%v", response.StatusCode, body, connects.Load(), err)
	}
	expectedTarget.Store("93.184.216.34:443")
	transport.mu.Lock()
	transport.preferred["huangguo.video|"+proxy.URL] = cdnRoute{address: "93.184.216.34", expires: time.Now().Add(time.Minute)}
	transport.mu.Unlock()
	response, err = d.doMediaRequest(request)
	if err != nil {
		t.Fatal("pinned CONNECT route failed", err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "synthetic-proxy-media" || connects.Load() < 2 {
		t.Fatal("pinned proxy route was not used", err)
	}
}

func TestHuangguoBrowserRecoversWrongDNSWithoutWeakeningTLS(t *testing.T) {
	for _, recoverable := range []bool{false, true} {
		t.Run(map[bool]string{false: "invalid-certificate", true: "valid-alternative"}[recoverable], func(t *testing.T) {
			var lookups atomic.Int32
			doh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				lookups.Add(1)
				if r.URL.Query().Get("name") != "huangguo.video" {
					t.Error("incorrect DNS query")
				}
				io.WriteString(w, `{"Status":0,"Answer":[{"type":1,"TTL":60,"data":"93.184.216.34"}]}`)
			}))
			defer doh.Close()
			d := sourceFixtureDownloader(t, nil)
			d.proxyRouter = &proxyRouter{mode: "direct"}
			transport := newHuangguoBrowserTransport(http.DefaultTransport, d)
			transport.resolvers = []*dnsResolver{{client: doh.Client(), endpoints: []string{doh.URL}, cache: map[string]dnsCacheEntry{}, inFlight: map[string]chan struct{}{}}}
			transport.newClient = func(proxy, host, address string) (browserHTTPClient, error) {
				if proxy != "" || host != "huangguo.video" || transport.insecure {
					t.Error("DNS fallback changed original origin or TLS verification")
				}
				return browserFixtureClient{do: func(r *fhttp.Request) (*fhttp.Response, error) {
					if r.URL.Host != "huangguo.video" {
						t.Error("DNS route replaced HTTP origin")
					}
					if address == "" || !recoverable {
						return nil, x509.UnknownAuthorityError{}
					}
					return &fhttp.Response{StatusCode: 200, Header: make(fhttp.Header), Body: io.NopCloser(strings.NewReader("synthetic-media")), Request: r}, nil
				}}, nil
			}
			request, _ := http.NewRequest(http.MethodGet, "https://huangguo.video/segment.ts", nil)
			response, err := transport.RoundTrip(request)
			if !recoverable {
				if err == nil || !isCertificateSourceError(err) {
					t.Fatal("invalid certificates were accepted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			response, err = transport.RoundTrip(request)
			if err != nil || lookups.Load() != 1 {
				t.Fatal("verified route was not cached", err, lookups.Load())
			}
			response.Body.Close()
		})
	}
}
