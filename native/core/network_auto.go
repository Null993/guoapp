package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type networkRouteKey struct{}
type automaticReadOnlyKey struct{}

type networkRoute struct {
	proxy *url.URL
}

type autoRoutePreference struct {
	index   int
	expires time.Time
}

type autoNetworkTransport struct {
	base          http.RoundTripper
	router        *proxyRouter
	record        func(diagnosticEvent)
	headerTimeout time.Duration
	fallbackDelay time.Duration
	mu            sync.Mutex
	preferred     map[string]autoRoutePreference
}

type autoNetworkBody struct {
	io.ReadCloser
	ctx      context.Context
	expected int64
	read     int64
	failed   func()
	once     sync.Once
}

func (body *autoNetworkBody) Read(buffer []byte) (int, error) {
	count, err := body.ReadCloser.Read(buffer)
	body.read += int64(count)
	if err == io.EOF && body.expected >= 0 && body.read < body.expected {
		err = io.ErrUnexpectedEOF
	}
	if err != nil && err != io.EOF && body.ctx.Err() == nil && automaticNetworkFailure(err) {
		body.once.Do(body.failed)
	}
	return count, err
}

func newAutoNetworkTransport(base http.RoundTripper, d *Downloader) *autoNetworkTransport {
	return &autoNetworkTransport{base: base, router: d.proxyRouter, record: d.recordDiagnostic, headerTimeout: 20 * time.Second, fallbackDelay: 3 * time.Second, preferred: map[string]autoRoutePreference{}}
}

func (router *proxyRouter) automaticRoutes(request *http.Request) ([]networkRoute, string) {
	router.mu.RLock()
	mode, manual := router.mode, router.manual
	router.mu.RUnlock()
	if mode != "auto" && mode != "" {
		return nil, ""
	}
	if _, exists := request.Context().Value(networkRouteKey{}).(networkRoute); exists {
		return nil, ""
	}
	host := request.URL.Hostname()
	if host == "localhost" || net.ParseIP(host).IsLoopback() {
		return nil, ""
	}
	primary, err := router.systemSettings().resolve(request)
	routes := []networkRoute{}
	seen := map[string]bool{}
	keys := []string{request.URL.Scheme + "://" + request.URL.Host}
	add := func(proxy *url.URL) {
		key := "direct"
		if proxy != nil {
			if proxy.Hostname() == "" {
				return
			}
			key = proxy.String()
		}
		if !seen[key] {
			seen[key] = true
			routes = append(routes, networkRoute{proxy: proxy})
			keys = append(keys, key)
		}
	}
	if err == nil {
		add(primary)
	}
	add(nil)
	if manual != nil {
		add(manual)
	}
	return routes, strings.Join(keys, "|")
}

func automaticRequestReplayable(request *http.Request) bool {
	if request.Body != nil && request.GetBody == nil {
		return false
	}
	if request.Method == http.MethodGet || request.Method == http.MethodHead {
		return true
	}
	if request.Method == http.MethodPost && request.Context().Value(automaticReadOnlyKey{}) == true {
		return true
	}
	if request.Method == http.MethodPost && providerSourceForURL(request.URL.String()) == sourceYeguo {
		for _, suffix := range []string{"/api/theater/exploreList", "/api/search/result", "/api/playlet/detail", "/api/playlet/play"} {
			if strings.HasSuffix(request.URL.Path, suffix) {
				return true
			}
		}
	}
	return false
}

func automaticNetworkFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) || isCertificateSourceError(err) {
		return true
	}
	var network net.Error
	if errors.As(err, &network) {
		return true
	}
	text := strings.ToLower(err.Error())
	for _, fragment := range []string{"connection reset", "connection refused", "network is unreachable", "no such host", "proxyconnect", "proxy error", "socks connect", "tls handshake timeout", "i/o timeout"} {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}

func (transport *autoNetworkTransport) attempt(request *http.Request, route networkRoute) (*http.Response, error) {
	ctx, cancel := context.WithCancel(context.WithValue(request.Context(), networkRouteKey{}, route))
	var mu sync.Mutex
	finished, expired := false, false
	timer := time.AfterFunc(transport.headerTimeout, func() {
		mu.Lock()
		defer mu.Unlock()
		if !finished {
			expired = true
			cancel()
		}
	})
	response, err := transport.base.RoundTrip(request.Clone(ctx))
	mu.Lock()
	finished = true
	timer.Stop()
	timedOut := expired
	mu.Unlock()
	if request.Context().Err() != nil {
		err = request.Context().Err()
	} else if timedOut {
		err = context.DeadlineExceeded
	}
	if err != nil {
		cancel()
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, err
	}
	response.Body = &limitedResponseBody{ReadCloser: response.Body, release: cancel}
	return response, nil
}

type autoNetworkResult struct {
	index    int
	response *http.Response
	err      error
	elapsed  time.Duration
}

func (transport *autoNetworkTransport) rememberResponse(request *http.Request, response *http.Response, routes []networkRoute, key string, index int) {
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return
	}
	transport.mu.Lock()
	if len(transport.preferred) >= 64 {
		clear(transport.preferred)
	}
	transport.preferred[key] = autoRoutePreference{index: index, expires: time.Now().Add(2 * time.Minute)}
	transport.mu.Unlock()
	expected := response.ContentLength
	if request.Method == http.MethodHead {
		expected = -1
	}
	response.Body = &autoNetworkBody{ReadCloser: response.Body, ctx: request.Context(), expected: expected, failed: func() {
		transport.mu.Lock()
		if len(transport.preferred) >= 64 {
			clear(transport.preferred)
		}
		transport.preferred[key] = autoRoutePreference{index: (index + 1) % len(routes), expires: time.Now().Add(2 * time.Minute)}
		transport.mu.Unlock()
		if transport.record != nil {
			transport.record(diagnosticEvent{Event: "network.auto_body_fallback", Level: "warning", Host: request.URL.Hostname(), Message: "媒体或站源正文传输中断，后续自动重试切换网络路径"})
		}
	}}
}

func (transport *autoNetworkTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if transport.router == nil || !automaticRequestReplayable(request) {
		return transport.base.RoundTrip(request)
	}
	routes, key := transport.router.automaticRoutes(request)
	if len(routes) == 0 {
		return transport.base.RoundTrip(request)
	}
	if len(routes) == 1 {
		return transport.base.RoundTrip(request.Clone(context.WithValue(request.Context(), networkRouteKey{}, routes[0])))
	}
	if request.Body != nil {
		defer request.Body.Close()
	}
	order := make([]int, 0, len(routes))
	transport.mu.Lock()
	preferred := transport.preferred[key]
	transport.mu.Unlock()
	if preferred.index < len(routes) && time.Now().Before(preferred.expires) {
		order = append(order, preferred.index)
	}
	for index := range routes {
		if len(order) == 0 || order[0] != index {
			order = append(order, index)
		}
	}
	done := make(chan struct{})
	defer close(done)
	results := make(chan autoNetworkResult)
	active := map[int]context.CancelFunc{}
	defer func() {
		for _, cancel := range active {
			cancel()
		}
	}()
	next := 0
	start := func(index int) {
		if next > 1 && transport.record != nil {
			mode := "代理"
			if routes[index].proxy == nil {
				mode = "直连"
			}
			transport.record(diagnosticEvent{Event: "network.auto_candidate", Level: "info", Host: request.URL.Hostname(), Message: "启动自动备用网络路径：" + mode})
		}
		ctx, cancel := context.WithCancel(request.Context())
		active[index] = cancel
		go func() {
			upstream := request.Clone(ctx)
			var response *http.Response
			var err error
			started := time.Now()
			if request.Body != nil {
				upstream.Body, err = request.GetBody()
			}
			if err == nil {
				response, err = transport.attempt(upstream, routes[index])
			}
			result := autoNetworkResult{index: index, response: response, err: err, elapsed: time.Since(started)}
			select {
			case results <- result:
			case <-done:
				if response != nil && response.Body != nil {
					response.Body.Close()
				}
				cancel()
			}
		}()
	}
	hedge := time.NewTimer(transport.fallbackDelay)
	defer hedge.Stop()
	hedgeReady := hedge.C
	allowFallback := false
	var failures []error
	var lastGateway *http.Response
	var lastWasGateway bool
	recordFallback := func(from int) {
		if transport.record == nil || next >= len(order) {
			return
		}
		old, new := "代理", "代理"
		if routes[from].proxy == nil {
			old = "直连"
		}
		if routes[order[next]].proxy == nil {
			new = "直连"
		}
		transport.record(diagnosticEvent{Event: "network.auto_fallback", Level: "warning", Source: providerSourceForURL(request.URL.String()), Host: request.URL.Hostname(), Message: "自动模式" + old + "未及时成功，尝试" + new + "；保留仍在等待的连接"})
	}
	for {
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		limit := 1
		if allowFallback {
			limit = 2
		}
		for len(active) < limit && next < len(order) {
			index := order[next]
			next++
			start(index)
		}
		if len(active) == 0 && next == len(order) {
			if lastWasGateway {
				return lastGateway, nil
			}
			return nil, fmt.Errorf("自动网络切换失败（已尝试 %d 条网络路径）: %w", len(order), errors.Join(failures...))
		}
		select {
		case <-request.Context().Done():
			return nil, request.Context().Err()
		case <-hedgeReady:
			hedgeReady = nil
			allowFallback = true
			recordFallback(order[0])
		case result := <-results:
			cancel := active[result.index]
			delete(active, result.index)
			response, err := result.response, result.err
			gateway := err == nil && (response.StatusCode == 407 || response.StatusCode == 502 || response.StatusCode == 504)
			if err == nil && routes[result.index].proxy != nil && response.StatusCode == 404 && response.ContentLength == 0 && proxyInterceptionSuspected(request.URL.Hostname(), response.StatusCode, response.Header, nil, result.elapsed) {
				gateway = true
			}
			if err == nil && !gateway {
				transport.rememberResponse(request, response, routes, key, result.index)
				response.Body = &limitedResponseBody{ReadCloser: response.Body, release: cancel}
				return response, nil
			}
			cancel()
			lastWasGateway = gateway
			if gateway {
				err = fmt.Errorf("网络网关 HTTP %d", response.StatusCode)
				response.Body.Close()
				stored := *response
				stored.Body = http.NoBody
				stored.ContentLength = 0
				lastGateway = &stored
			}
			if err != nil && !gateway && !automaticNetworkFailure(err) {
				return nil, err
			}
			failures = append(failures, err)
			allowFallback = true
			recordFallback(result.index)
		}
	}
}

func (transport *autoNetworkTransport) CloseIdleConnections() {
	transport.mu.Lock()
	clear(transport.preferred)
	transport.mu.Unlock()
	if closer, ok := transport.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
