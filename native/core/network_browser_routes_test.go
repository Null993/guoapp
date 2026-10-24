package core

import (
	"context"
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

func browserRouteFixture(t *testing.T) *sourceBrowserTransport {
	t.Helper()
	doh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"Status":0,"Answer":[{"type":1,"TTL":60,"data":"93.184.216.34"},{"type":1,"TTL":60,"data":"93.184.216.35"}]}`)
	}))
	t.Cleanup(doh.Close)
	d := sourceFixtureDownloader(t, nil)
	d.proxyRouter = &proxyRouter{mode: "direct"}
	transport := newHuangguoBrowserTransport(http.DefaultTransport, d)
	transport.resolvers = []*dnsResolver{{client: doh.Client(), endpoints: []string{doh.URL}, cache: map[string]dnsCacheEntry{}, inFlight: map[string]chan struct{}{}}}
	return transport
}

func TestBrowserRoutesKeepSlowHealthyOriginWhileAlternativesFail(t *testing.T) {
	transport := browserRouteFixture(t)
	transport.newClient = func(proxy, host, address string) (browserHTTPClient, error) {
		return browserFixtureClient{do: func(r *fhttp.Request) (*fhttp.Response, error) {
			if address != "" {
				return nil, errors.New("synthetic unavailable alternative")
			}
			select {
			case <-time.After(3200 * time.Millisecond):
				return &fhttp.Response{StatusCode: 200, Header: make(fhttp.Header), Body: io.NopCloser(strings.NewReader("slow healthy origin")), Request: r}, nil
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://huangguo.video/series/synthetic", nil)
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal("healthy response after the old 3s threshold was canceled", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "slow healthy origin" {
		t.Fatal("slow response body lost", err)
	}
}

func TestBrowserRoutesCancelLoserAndKeepWinningBodyAfterHeaderBudget(t *testing.T) {
	transport := browserRouteFixture(t)
	transport.fallbackDelay = 10 * time.Millisecond
	transport.headerTimeout = 100 * time.Millisecond
	loserCanceled := make(chan struct{})
	transport.newClient = func(proxy, host, address string) (browserHTTPClient, error) {
		return browserFixtureClient{do: func(r *fhttp.Request) (*fhttp.Response, error) {
			if address == "" {
				<-r.Context().Done()
				close(loserCanceled)
				return nil, r.Context().Err()
			}
			return &fhttp.Response{StatusCode: 200, Header: make(fhttp.Header), Body: &contextFixtureBody{ctx: r.Context()}, Request: r}, nil
		}}, nil
	}
	request, _ := http.NewRequest(http.MethodGet, "https://huangguo.video/segment.ts", nil)
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	select {
	case <-loserCanceled:
	case <-time.After(time.Second):
		t.Fatal("losing connection remained active")
	}
	time.Sleep(150 * time.Millisecond)
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal("header budget canceled the winning body", err)
	}
}

type browserCloseFixture struct{ closed chan struct{} }

func (body *browserCloseFixture) Read([]byte) (int, error) { return 0, io.EOF }
func (body *browserCloseFixture) Close() error {
	close(body.closed)
	return nil
}

func TestBrowserRoutesCloseLateResponseAfterParentCancellation(t *testing.T) {
	transport := browserRouteFixture(t)
	started, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	transport.newClient = func(proxy, host, address string) (browserHTTPClient, error) {
		return browserFixtureClient{do: func(r *fhttp.Request) (*fhttp.Response, error) {
			close(started)
			<-release
			return &fhttp.Response{StatusCode: 200, Header: make(fhttp.Header), Body: &browserCloseFixture{closed: closed}, Request: r}, nil
		}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://huangguo.video/segment.ts", nil)
	completed := make(chan error, 1)
	go func() { _, err := transport.RoundTrip(request); completed <- err }()
	<-started
	cancel()
	if err := <-completed; !errors.Is(err, context.Canceled) {
		t.Fatal("parent cancellation was replaced", err)
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("late response body leaked")
	}
}

func TestBrowserRoutesBoundConcurrencyAndTotalHeaderWait(t *testing.T) {
	transport := browserRouteFixture(t)
	transport.fallbackDelay = time.Millisecond
	transport.headerTimeout = 100 * time.Millisecond
	var active, peak atomic.Int32
	transport.newClient = func(proxy, host, address string) (browserHTTPClient, error) {
		return browserFixtureClient{do: func(r *fhttp.Request) (*fhttp.Response, error) {
			n := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
			}
			<-r.Context().Done()
			return nil, r.Context().Err()
		}}, nil
	}
	request, _ := http.NewRequest(http.MethodGet, "https://huangguo.video/segment.ts", nil)
	started := time.Now()
	if _, err := transport.RoundTrip(request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("unbounded header wait", err)
	}
	if peak.Load() != 2 || time.Since(started) > time.Second {
		t.Fatal("route concurrency or budget incorrect", peak.Load(), time.Since(started))
	}
}

func TestBrowserRoutesSlowHuangguoDetailCompletesBackgroundMetadata(t *testing.T) {
	previous := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = previous }()
	engine := sourceFixtureEngine(t, metadataDetailTransport(nil))
	engine.downloader.cfg.HuangguoVideoURL = "https://huangguo.video"
	if _, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceHuangguoVideo, Page: 1}); err != nil {
		t.Fatal(err)
	}
	transport := browserRouteFixture(t)
	transport.newClient = func(proxy, host, address string) (browserHTTPClient, error) {
		return browserFixtureClient{do: func(r *fhttp.Request) (*fhttp.Response, error) {
			if address != "" {
				return nil, errors.New("synthetic unavailable alternative")
			}
			deadline, ok := r.Context().Deadline()
			if !ok || time.Until(deadline) < 20*time.Second {
				t.Error("background detail did not retain its full caller budget")
			}
			select {
			case <-time.After(3200 * time.Millisecond):
				body := `<h1>合成短剧</h1><meta name="description" content="完整资料"><div data-hls="https://media.example.test/master.m3u8"></div>`
				return &fhttp.Response{StatusCode: 200, Header: make(fhttp.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}}, nil
	}
	engine.downloader.client.Transport = transport
	ctx := context.WithValue(context.Background(), backgroundCatalogKey{}, true)
	if err := engine.updateSource(ctx, sourceHuangguoVideo, "continuousMetadata"); err != nil {
		t.Fatal("background metadata update rejected a slow healthy source", err)
	}
	items := engine.nativeCached(sourceHuangguoVideo).Items
	if len(items) != 1 || items[0].Episodes != 1 || items[0].Description != "完整资料" {
		t.Fatalf("background metadata was not saved: %+v", items)
	}
	engine.mu.Lock()
	record := engine.sourceRecords[sourceHuangguoVideo]
	engine.mu.Unlock()
	if record.MetadataChecked[items[0].ID].IsZero() || !record.MetadataRetryAt[items[0].ID].IsZero() {
		t.Fatal("successful metadata remained pending retry")
	}
}
