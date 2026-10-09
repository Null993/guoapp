package core

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

const proxyInterceptionWindow = 50 * time.Millisecond

type requestTiming struct {
	started time.Time
}

type requestTimingKey struct{}

type proxyInterceptionError struct {
	host    string
	status  int
	elapsed time.Duration
}

func (failure *proxyInterceptionError) Error() string {
	return fmt.Sprintf("%s 返回 %dms 空 %d（无 Server 头），疑似本机代理拦截了该域名。请在“网络与资源”里改用直连，或换用不拦截该域名的代理节点/规则。",
		failure.host, max(1, int(failure.elapsed.Milliseconds())), failure.status)
}

func requestTimingContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestTimingKey{}, &requestTiming{started: time.Now()})
}

func requestElapsed(ctx context.Context) time.Duration {
	timing, _ := ctx.Value(requestTimingKey{}).(*requestTiming)
	if timing == nil || timing.started.IsZero() {
		return 0
	}
	elapsed := time.Since(timing.started)
	if elapsed <= 0 {
		return time.Nanosecond
	}
	return elapsed
}

func responseElapsed(request *http.Request, response *http.Response) time.Duration {
	if response != nil && response.Request != nil {
		if elapsed := requestElapsed(response.Request.Context()); elapsed > 0 {
			return elapsed
		}
	}
	if request != nil {
		return requestElapsed(request.Context())
	}
	return 0
}

func proxyInterceptedResponse(status int, header http.Header, body []byte, elapsed time.Duration) bool {
	switch status {
	case http.StatusForbidden, http.StatusNotFound, http.StatusProxyAuthRequired, http.StatusUnavailableForLegalReasons:
	default:
		return false
	}
	if len(body) > 0 || strings.TrimSpace(header.Get("Server")) != "" {
		return false
	}
	return elapsed > 0 && elapsed < proxyInterceptionWindow
}

func proxyInterceptedHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if stripped, _, err := net.SplitHostPort(host); err == nil {
		host = stripped
	}
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".local") || !strings.Contains(host, ".") {
		return false
	}
	address := net.ParseIP(strings.Trim(host, "[]"))
	if address == nil {
		return true
	}
	return !address.IsLoopback() && !address.IsPrivate() && !address.IsLinkLocalUnicast() &&
		!address.IsLinkLocalMulticast() && !address.IsUnspecified()
}

func proxyInterceptionSuspected(host string, status int, header http.Header, body []byte, elapsed time.Duration) bool {
	return proxyInterceptedHost(host) && proxyInterceptedResponse(status, header, body, elapsed)
}

func describeHTTPFailure(host string, status int, header http.Header, body []byte, elapsed time.Duration) error {
	if proxyInterceptionSuspected(host, status, header, body, elapsed) {
		return &proxyInterceptionError{host: host, status: status, elapsed: elapsed}
	}
	return fmt.Errorf("%s HTTP %d", host, status)
}
