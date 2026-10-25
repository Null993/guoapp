package core

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type browserRouteResult struct {
	id       int
	route    cdnRoute
	response *http.Response
	err      error
	elapsed  time.Duration
}

func (transport *sourceBrowserTransport) discoverBrowserRoutes(ctx context.Context, host string, routes chan<- cdnRoute) {
	defer close(routes)
	for _, resolver := range transport.resolvers {
		if ctx.Err() != nil {
			return
		}
		lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		entry, err := resolver.lookupEntry(lookupCtx, host, "")
		cancel()
		if err != nil {
			continue
		}
		for _, address := range entry.addresses {
			select {
			case routes <- cdnRoute{address: address, expires: entry.expires}:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (transport *sourceBrowserTransport) roundTripRoutes(request *http.Request, proxyURL, key string) (*http.Response, error) {
	ctx, cancel := context.WithCancel(request.Context())
	discoveryCtx, cancelDiscovery := context.WithCancel(ctx)
	defer cancelDiscovery()
	done := make(chan struct{})
	defer close(done)
	retained := false
	defer func() {
		if !retained {
			cancel()
		}
	}()
	var headerMu sync.Mutex
	headersDone := false
	timer := time.AfterFunc(transport.headerTimeout, func() {
		headerMu.Lock()
		defer headerMu.Unlock()
		if !headersDone {
			cancel()
		}
	})
	defer timer.Stop()
	headerError := func() error {
		if err := request.Context().Err(); err != nil {
			return err
		}
		return fmt.Errorf("站源响应头等待超时（慢线路与备用地址均未及时返回）: %w", context.DeadlineExceeded)
	}
	transport.mu.Lock()
	preferred := transport.preferred[key]
	transport.mu.Unlock()
	queue := []cdnRoute{{}}
	if preferred.address != "" && time.Now().Before(preferred.expires) {
		queue[0] = preferred
	}
	results := make(chan browserRouteResult)
	active := map[int]context.CancelFunc{}
	defer func() {
		for _, stop := range active {
			stop()
		}
	}()
	tried := map[string]bool{}
	sequence := 0
	start := func(route cdnRoute) {
		sequence++
		id := sequence
		attemptCtx, stop := context.WithCancel(ctx)
		active[id] = stop
		tried[route.address] = true
		go func() {
			started := time.Now()
			response, err := transport.roundTripAddress(request.Clone(attemptCtx), proxyURL, route.address)
			select {
			case results <- browserRouteResult{id: id, route: route, response: response, err: err, elapsed: time.Since(started)}:
			case <-done:
				if response != nil && response.Body != nil {
					response.Body.Close()
				}
				stop()
			}
		}()
	}
	replayable := request.Body == nil || request.GetBody != nil
	fallbackStarted, discoveryDone := false, !replayable
	var candidates <-chan cdnRoute
	fallback := func() {
		if fallbackStarted || !replayable {
			return
		}
		fallbackStarted = true
		if !tried[""] {
			queue = append(queue, cdnRoute{})
		}
		found := make(chan cdnRoute)
		candidates = found
		go transport.discoverBrowserRoutes(discoveryCtx, request.URL.Hostname(), found)
	}
	hedge := time.NewTimer(transport.fallbackDelay)
	defer hedge.Stop()
	var hedgeReady <-chan time.Time
	if replayable {
		hedgeReady = hedge.C
	}
	var firstErr error
	for {
		for len(active) < 2 && len(queue) > 0 {
			route := queue[0]
			queue = queue[1:]
			if tried[route.address] || route.address != "" && len(tried) >= 5 {
				continue
			}
			start(route)
		}
		if len(active) == 0 {
			fallback()
			if discoveryDone && len(queue) == 0 {
				return nil, firstErr
			}
		}
		select {
		case <-ctx.Done():
			return nil, headerError()
		case <-hedgeReady:
			hedgeReady = nil
			fallback()
		case route, open := <-candidates:
			if !open {
				candidates = nil
				discoveryDone = true
			} else {
				queue = append(queue, route)
			}
		case result := <-results:
			stop := active[result.id]
			delete(active, result.id)
			if result.err == nil && proxyURL != "" && result.response.StatusCode == http.StatusNotFound && result.response.ContentLength == 0 && proxyInterceptionSuspected(request.URL.Hostname(), result.response.StatusCode, result.response.Header, nil, result.elapsed) {
				result.response.Body.Close()
				result.err = &proxyInterceptionError{host: request.URL.Hostname(), status: result.response.StatusCode, elapsed: result.elapsed}
			}
			if result.err != nil {
				stop()
				if firstErr == nil {
					firstErr = result.err
				}
				fallback()
				continue
			}
			headerMu.Lock()
			headersDone = true
			timer.Stop()
			failure := ctx.Err()
			headerMu.Unlock()
			if failure != nil {
				result.response.Body.Close()
				stop()
				return nil, headerError()
			}
			if result.route.address != "" {
				transport.mu.Lock()
				if len(transport.preferred) >= 32 {
					for old := range transport.preferred {
						delete(transport.preferred, old)
						break
					}
				}
				transport.preferred[key] = result.route
				transport.mu.Unlock()
				if transport.record != nil {
					transport.record(diagnosticEvent{Event: "network.browser_dns_route", Level: "info", Source: transport.requestSource(request),
						Host: request.URL.Hostname(), Client: huangguoBrowserProfile, Message: "已通过原域名 TLS 校验的备用 DNS 线路: " + result.route.address})
				}
			}
			retained = true
			result.response.Body = &limitedResponseBody{ReadCloser: result.response.Body, release: func() { stop(); cancel() }}
			return result.response, nil
		}
	}
}
