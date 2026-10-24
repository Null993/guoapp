package core

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func autoNetworkFixture(t *testing.T, do func(*http.Request, *url.URL) (*http.Response, error)) *autoNetworkTransport {
	t.Helper()
	router := &proxyRouter{mode: "auto", systemOverride: true, system: nativeSystemProxy{HTTP: "http://system.test:8080", HTTPS: "http://system.test:8080"}}
	base := sourceFixtureTransport(func(r *http.Request) (*http.Response, error) {
		proxy, err := router.proxy(r)
		if err != nil {
			return nil, err
		}
		return do(r, proxy)
	})
	return &autoNetworkTransport{base: base, router: router, headerTimeout: 150 * time.Millisecond, fallbackDelay: 10 * time.Millisecond, preferred: map[string]autoRoutePreference{}}
}

func autoNetworkResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: r}
}

func autoNetworkDialError() error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("synthetic unavailable route")}
}

func TestAutomaticNetworkProxyFailureFallsBackAndRemembersDirect(t *testing.T) {
	var proxyAttempts, directAttempts atomic.Int32
	transport := autoNetworkFixture(t, func(r *http.Request, proxy *url.URL) (*http.Response, error) {
		if proxy != nil {
			proxyAttempts.Add(1)
			return nil, autoNetworkDialError()
		}
		directAttempts.Add(1)
		return autoNetworkResponse(r, 200, "synthetic media"), nil
	})
	for range 2 {
		r, _ := http.NewRequest("GET", "https://source.test/video", nil)
		response, err := transport.RoundTrip(r)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(body) != "synthetic media" {
			t.Fatal(string(body), err)
		}
	}
	if proxyAttempts.Load() != 1 || directAttempts.Load() != 2 || transport.router.mode != "auto" {
		t.Fatal("fallback changed user mode or was not remembered")
	}
}

func TestAutomaticNetworkDirectFallsBackToConfiguredProxy(t *testing.T) {
	var attempts []string
	transport := autoNetworkFixture(t, func(r *http.Request, proxy *url.URL) (*http.Response, error) {
		if proxy == nil {
			attempts = append(attempts, "direct")
			return nil, autoNetworkDialError()
		}
		attempts = append(attempts, proxy.Hostname())
		return autoNetworkResponse(r, 200, "ok"), nil
	})
	transport.router.system = nativeSystemProxy{PAC: true}
	transport.router.manual, _ = url.Parse("http://configured.test:7890")
	r, _ := http.NewRequest("GET", "https://source.test/detail", nil)
	response, err := transport.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if strings.Join(attempts, ",") != "direct,configured.test" {
		t.Fatal(attempts)
	}
}

func TestAutomaticNetworkHonorsExplicitModesAndSiteDenials(t *testing.T) {
	for _, scenario := range []struct {
		mode     string
		status   int
		failure  bool
		expected int
	}{
		{"direct", 0, true, 1}, {"manual", 0, true, 1}, {"auto", 403, false, 1}, {"auto", 404, false, 1}, {"auto", 429, false, 1}, {"auto", 407, false, 2}, {"auto", 502, false, 2},
	} {
		t.Run(scenario.mode+http.StatusText(scenario.status), func(t *testing.T) {
			calls := 0
			transport := autoNetworkFixture(t, func(r *http.Request, proxy *url.URL) (*http.Response, error) {
				calls++
				if scenario.failure {
					return nil, autoNetworkDialError()
				}
				return autoNetworkResponse(r, scenario.status, "denied"), nil
			})
			transport.router.mode = scenario.mode
			transport.router.manual, _ = url.Parse("http://system.test:8080")
			r, _ := http.NewRequest("GET", "https://source.test/detail", nil)
			response, _ := transport.RoundTrip(r)
			if response != nil {
				response.Body.Close()
			}
			if calls != scenario.expected {
				t.Fatal(calls, scenario.expected)
			}
		})
	}
}

func TestAutomaticNetworkHeaderTimeoutKeepsWinningBodyAndParentCancellation(t *testing.T) {
	var canceled atomic.Bool
	transport := autoNetworkFixture(t, func(r *http.Request, proxy *url.URL) (*http.Response, error) {
		if proxy != nil {
			<-r.Context().Done()
			canceled.Store(true)
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &contextFixtureBody{ctx: r.Context()}, ContentLength: -1, Request: r}, nil
	})
	r, _ := http.NewRequest("GET", "https://source.test/media", nil)
	response, err := transport.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for !canceled.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !canceled.Load() {
		t.Fatal("losing connection not canceled")
	}
	time.Sleep(180 * time.Millisecond)
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal("header timer canceled winning body", err)
	}
	response.Body.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = r.WithContext(ctx)
	if _, err := transport.RoundTrip(r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestAutomaticNetworkDoesNotReplayMutationOrUnreplayableBody(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		calls := 0
		transport := autoNetworkFixture(t, func(r *http.Request, proxy *url.URL) (*http.Response, error) {
			calls++
			return nil, autoNetworkDialError()
		})
		r, _ := http.NewRequest(method, "https://source.test/signup", io.NopCloser(strings.NewReader("payload")))
		transport.RoundTrip(r)
		if calls != 1 {
			t.Fatal("unsafe body replay", method, calls)
		}
	}
}

func TestAutomaticNetworkInterruptedBodySwitchesSubsequentRequest(t *testing.T) {
	var attempts []bool
	transport := autoNetworkFixture(t, func(r *http.Request, proxy *url.URL) (*http.Response, error) {
		attempts = append(attempts, proxy != nil)
		response := autoNetworkResponse(r, 200, "partial")
		if proxy != nil {
			response.ContentLength += 10
		}
		return response, nil
	})
	r, _ := http.NewRequest("GET", "https://source.test/segment", nil)
	response, err := transport.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadAll(response.Body); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	response.Body.Close()
	response, err = transport.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(attempts) != 2 || !attempts[0] || attempts[1] {
		t.Fatal(attempts)
	}
}

func TestAutomaticNetworkHeadPreservesContentLength(t *testing.T) {
	transport := autoNetworkFixture(t, func(r *http.Request, proxy *url.URL) (*http.Response, error) {
		response := autoNetworkResponse(r, 200, "")
		response.ContentLength = 12345
		return response, nil
	})
	r, _ := http.NewRequest("HEAD", "https://source.test/media", nil)
	response, err := transport.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticNetworkReplaysOnlyMarkedReadOnlyPost(t *testing.T) {
	var bodies []string
	transport := autoNetworkFixture(t, func(r *http.Request, proxy *url.URL) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		bodies = append(bodies, string(body))
		if proxy != nil {
			return nil, autoNetworkDialError()
		}
		return autoNetworkResponse(r, 200, "ok"), nil
	})
	ctx := context.WithValue(context.Background(), automaticReadOnlyKey{}, true)
	r, _ := http.NewRequestWithContext(ctx, "POST", "https://source.test/detail", strings.NewReader("signed read payload"))
	response, err := transport.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(bodies) != 2 || bodies[0] != bodies[1] || bodies[0] != "signed read payload" {
		t.Fatal(bodies)
	}
}

func TestAutomaticNetworkPACWithoutProxyStillUsesDirect(t *testing.T) {
	transport := autoNetworkFixture(t, func(r *http.Request, proxy *url.URL) (*http.Response, error) {
		if proxy != nil {
			t.Fatal("unexpected proxy")
		}
		return autoNetworkResponse(r, 200, "ok"), nil
	})
	transport.router.system = nativeSystemProxy{PAC: true}
	r, _ := http.NewRequest("GET", "https://source.test/detail", nil)
	response, err := transport.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
}

func TestAutomaticNetworkSuspectedEmptyProxy404FallsBack(t *testing.T) {
	calls := 0
	transport := autoNetworkFixture(t, func(r *http.Request, proxy *url.URL) (*http.Response, error) {
		calls++
		if proxy != nil {
			time.Sleep(time.Millisecond)
			return autoNetworkResponse(r, 404, ""), nil
		}
		return autoNetworkResponse(r, 200, "ok"), nil
	})
	r, _ := http.NewRequest("GET", "https://huangguo.video/series/synthetic", nil)
	response, err := transport.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || calls != 2 {
		t.Fatal(response.StatusCode, calls)
	}
}

func TestAutomaticNetworkBackgroundMetadataSurvivesProxyFailure(t *testing.T) {
	previous := buildAllSources
	buildAllSources = "true"
	t.Cleanup(func() { buildAllSources = previous })
	fixture := metadataDetailTransport(func(r *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(r, http.StatusOK, `<h1>合成短剧</h1><meta name="description" content="完整资料"><div data-hls="https://media.example.test/master.m3u8"></div>`), nil
	})
	engine := sourceFixtureEngine(t, fixture)
	engine.downloader.cfg.HuangguoVideoURL = "https://huangguo.video"
	if _, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceHuangguoVideo, Page: 1}); err != nil {
		t.Fatal(err)
	}
	var fallback atomic.Bool
	transport := autoNetworkFixture(t, func(r *http.Request, proxy *url.URL) (*http.Response, error) {
		if proxy != nil {
			return nil, autoNetworkDialError()
		}
		fallback.Store(true)
		return fixture.RoundTrip(r)
	})
	engine.downloader.client.Transport = transport
	ctx := context.WithValue(context.Background(), backgroundCatalogKey{}, true)
	if err := engine.updateSource(ctx, sourceHuangguoVideo, "continuousMetadata"); err != nil {
		t.Fatal(err)
	}
	items := engine.nativeCached(sourceHuangguoVideo).Items
	if !fallback.Load() || len(items) != 1 || items[0].Episodes != 1 || items[0].Description != "完整资料" {
		t.Fatalf("fallback update not saved: %+v", items)
	}
}

func TestAutomaticNetworkHedgesWithoutCancelingSlowHealthyPrimary(t *testing.T) {
	var alternative atomic.Bool
	transport := autoNetworkFixture(t, func(r *http.Request, proxy *url.URL) (*http.Response, error) {
		if proxy == nil {
			alternative.Store(true)
			return nil, autoNetworkDialError()
		}
		select {
		case <-time.After(80 * time.Millisecond):
			return autoNetworkResponse(r, 200, "slow healthy primary"), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
	r, _ := http.NewRequest("GET", "https://source.test/detail", nil)
	response, err := transport.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "slow healthy primary" || !alternative.Load() {
		t.Fatal("slow primary canceled or alternative not attempted", string(body), err)
	}
}

func TestAutomaticNetworkBoundsConcurrentRoutesAndClosesLateResponse(t *testing.T) {
	var active, peak atomic.Int32
	transport := autoNetworkFixture(t, func(r *http.Request, proxy *url.URL) (*http.Response, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	transport.router.manual, _ = url.Parse("http://backup.test:7890")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", "https://source.test/detail", nil)
	if _, err := transport.RoundTrip(r); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if peak.Load() != 2 {
		t.Fatal("unbounded concurrency", peak.Load())
	}

	release, closed := make(chan struct{}), make(chan struct{})
	late := autoNetworkFixture(t, func(r *http.Request, proxy *url.URL) (*http.Response, error) {
		if proxy != nil {
			<-release
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &browserCloseFixture{closed: closed}, Request: r}, nil
		}
		return autoNetworkResponse(r, 200, "winner"), nil
	})
	r, _ = http.NewRequest("GET", "https://source.test/detail", nil)
	response, err := late.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	close(release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("late response leaked")
	}
}
