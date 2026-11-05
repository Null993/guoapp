package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

func TestHuangdouUpdateContinuesImportedThousandthPage(t *testing.T) {
	requests := 0
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/drama/list" {
			t.Fatal(request.URL.Path)
		}
		requests++
		return sourceFixtureResponse(request, 200, `{"status":"y","data":{"list":[]}}`), nil
	})
	source := sourceHuangdou
	engine.catalogs[source] = []nativeDrama{{ID: "huangdou:123", Source: source, Title: "合成旧目录", Episodes: 1, Description: "完整资料"}}
	engine.catalogStates[source] = nativeCatalogState{Page: 1000, HasMore: true}
	engine.sourceRecords[source] = nativeSourceRecord{CatalogStarted: true, MetadataChecked: map[string]time.Time{"huangdou:123": time.Now()}}
	if err := engine.runContinuousSourceUpdate(context.Background(), source, true); err != nil {
		t.Fatal(err)
	}
	state := engine.nativeCached(source)
	if requests != 1 || state.Page != 1001 || state.HasMore || len(state.Items) != 1 {
		t.Fatal("imported pagination did not reach its actual empty tail", requests, state.Page, state.HasMore, len(state.Items))
	}
}

func TestAutomaticYeguoPlain400SwitchesPath(t *testing.T) {
	for _, contentType := range []string{"text/plain; charset=utf-8", "application/json"} {
		t.Run(contentType, func(t *testing.T) {
			calls := 0
			transport := autoNetworkFixture(t, func(request *http.Request, proxy *url.URL) (*http.Response, error) {
				calls++
				if proxy != nil {
					response := autoNetworkResponse(request, 400, "invalid request")
					response.Header.Set("Content-Type", contentType)
					return response, nil
				}
				return autoNetworkResponse(request, 200, `{"status":"1"}`), nil
			})
			request, _ := http.NewRequest(http.MethodPost, "https://www.yeguodj.com/api.php/api/theater/exploreList", strings.NewReader("page=54&limit=20"))
			response, err := transport.RoundTrip(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if contentType == "application/json" {
				if response.StatusCode != 400 || calls != 1 {
					t.Fatal("source JSON validation error was treated as a failed network path", response.StatusCode, calls)
				}
			} else if response.StatusCode != 200 || calls != 2 {
				t.Fatal("plain gateway rejection prevented automatic fallback", response.StatusCode, calls)
			}
		})
	}
}

func TestAutomaticNetworkRetriesNestedEmptyRouteFailure(t *testing.T) {
	calls := 0
	transport := autoNetworkFixture(t, func(request *http.Request, proxy *url.URL) (*http.Response, error) {
		calls++
		if proxy != nil {
			return nil, &url.Error{Op: "Get", URL: request.URL.String(), Err: &proxyInterceptionError{host: request.URL.Hostname(), status: http.StatusNotFound, elapsed: time.Millisecond}}
		}
		return autoNetworkResponse(request, 200, "synthetic recovered media"), nil
	})
	request, _ := http.NewRequest(http.MethodGet, "https://cdn.huangguoai.fit/synthetic.ts", nil)
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || calls != 2 {
		t.Fatal("nested DNS failure prevented trying the next network mode", response.StatusCode, calls)
	}
}

func TestSourceBrowserRecoversDirectHTTPFailureThroughDNS(t *testing.T) {
	for _, source := range []string{sourceHuangguoVideo, sourceYeguo} {
		t.Run(source, func(t *testing.T) {
			doh := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				io.WriteString(writer, `{"Status":0,"Answer":[{"type":1,"TTL":60,"data":"93.184.216.34"}]}`)
			}))
			defer doh.Close()
			d := sourceFixtureDownloader(t, nil)
			d.proxyRouter = &proxyRouter{mode: "direct"}
			transport := newSourceBrowserTransport(http.DefaultTransport, d, source, "")
			transport.resolvers = []*dnsResolver{{client: doh.Client(), endpoints: []string{doh.URL}, cache: map[string]dnsCacheEntry{}, inFlight: map[string]chan struct{}{}}}
			var calls atomic.Int32
			transport.newClient = func(proxy, host, address string) (browserHTTPClient, error) {
				if proxy != "" || transport.insecure {
					t.Error("direct fallback changed user network mode or TLS verification")
				}
				return browserFixtureClient{do: func(request *fhttp.Request) (*fhttp.Response, error) {
					calls.Add(1)
					if address == "" {
						status := http.StatusNotFound
						header := make(fhttp.Header)
						if source == sourceYeguo {
							status = http.StatusBadRequest
							header.Set("Content-Type", "text/plain; charset=utf-8")
						}
						return &fhttp.Response{StatusCode: status, ContentLength: 0, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
					}
					return &fhttp.Response{StatusCode: 200, Header: make(fhttp.Header), Body: io.NopCloser(strings.NewReader("synthetic recovered response")), Request: request}, nil
				}}, nil
			}
			method, address := http.MethodGet, "https://huangguo.video/series/synthetic"
			var body io.Reader
			if source == sourceYeguo {
				method, address, body = http.MethodPost, "https://www.yeguodj.com/api.php/api/theater/exploreList", strings.NewReader("page=54&limit=20")
			}
			request, _ := http.NewRequest(method, address, body)
			response, err := transport.RoundTrip(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != 200 || calls.Load() != 2 {
				t.Fatal("HTTP response bypassed direct DNS recovery", response.StatusCode, calls.Load())
			}
		})
	}
}
