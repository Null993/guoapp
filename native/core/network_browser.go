package core

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

const huangguoBrowserProfile = "Chrome 150"

type browserHTTPClient interface {
	Do(*fhttp.Request) (*fhttp.Response, error)
	CloseIdleConnections()
}

type sourceBrowserTransport struct {
	base          http.RoundTripper
	router        *proxyRouter
	host          string
	source        string
	pageHosts     map[string]string
	userAgent     func(*http.Request) string
	insecure      bool
	record        func(diagnosticEvent)
	mu            sync.Mutex
	clients       map[string]browserHTTPClient
	preferred     map[string]cdnRoute
	resolvers     []*dnsResolver
	newClient     func(string, string, string) (browserHTTPClient, error)
	headerTimeout time.Duration
	fallbackDelay time.Duration
}

func newHuangguoBrowserTransport(base http.RoundTripper, downloader *Downloader) *sourceBrowserTransport {
	configured, _ := url.Parse(downloader.providerBaseURL(sourceHuangguoVideo))
	transport := newSourceBrowserTransport(base, downloader, sourceHuangguoVideo, configured.Host)
	transport.pageHosts = map[string]string{}
	for _, source := range []string{sourceYeguo, sourceDSD} {
		if page, err := url.Parse(downloader.providerBaseURL(source)); err == nil {
			transport.pageHosts[source] = page.Host
		}
	}
	return transport
}

func newSourceBrowserTransport(base http.RoundTripper, downloader *Downloader, source, host string) *sourceBrowserTransport {
	router := downloader.proxyRouter
	if router == nil {
		router = &proxyRouter{}
	}
	transport := &sourceBrowserTransport{
		base: base, router: router, host: host, source: source,
		insecure: downloader.insecureTLS(), record: downloader.recordDiagnostic,
		clients:       make(map[string]browserHTTPClient),
		preferred:     make(map[string]cdnRoute),
		headerTimeout: 20 * time.Second, fallbackDelay: 3 * time.Second,
	}
	transport.userAgent = func(request *http.Request) string {
		if access, configured := downloader.accessForRequest(request); configured && access.UserAgent != "" {
			return access.UserAgent
		}
		if credentials, _ := request.Context().Value(providerMediaCredentialsKey{}).(*providerMediaCredentials); credentials != nil && credentials.origin == providerMediaOrigin(request.URL) {
			return credentials.userAgent
		}
		return ""
	}
	if cdn, ok := base.(*cdnTransport); ok && cdn.resolver != nil {
		endpoints := append([]string(nil), cdn.resolver.endpoints...)
		sort.SliceStable(endpoints, func(i, j int) bool {
			return strings.Contains(endpoints[i], "dns.google") && !strings.Contains(endpoints[j], "dns.google")
		})
		for _, endpoint := range endpoints {
			transport.resolvers = append(transport.resolvers, &dnsResolver{
				client: cdn.resolver.client, endpoints: []string{endpoint},
				cache: map[string]dnsCacheEntry{}, inFlight: map[string]chan struct{}{},
			})
		}
	}
	transport.newClient = transport.createClient
	return transport
}

func (transport *sourceBrowserTransport) matches(request *http.Request) bool {
	if request.URL.Scheme != "http" && request.URL.Scheme != "https" {
		return false
	}
	method := request.Method == http.MethodGet || request.Method == http.MethodHead
	if transport.requestSource(request) == sourceYeguo && request.Method == http.MethodPost && request.GetBody != nil {
		for _, route := range []string{"/api/theater/exploreList", "/api/search/result", "/api/playlet/detail", "/api/playlet/play"} {
			method = method || strings.HasSuffix(request.URL.Path, route)
		}
	}
	if !method {
		return false
	}
	if strings.EqualFold(request.URL.Host, transport.host) {
		return true
	}
	for source, host := range transport.pageHosts {
		if strings.EqualFold(request.URL.Host, host) || providerSourceForURL(request.URL.String()) == source {
			return true
		}
	}
	if transport.source == sourceYeguo {
		return providerSourceForURL(request.URL.String()) == sourceYeguo
	}
	return huangguoBrowserHost(request.URL.Hostname())
}

func (transport *sourceBrowserTransport) requestSource(request *http.Request) string {
	for source, host := range transport.pageHosts {
		if strings.EqualFold(request.URL.Host, host) || providerSourceForURL(request.URL.String()) == source {
			return source
		}
	}
	return transport.source
}

func huangguoBrowserHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return host == "huangguoai.fit" || host == "huangguo.video" || strings.HasSuffix(host, ".huangguo.video")
}

func (transport *sourceBrowserTransport) createClient(proxy, host, address string) (browserHTTPClient, error) {
	idleTimeout := 90 * time.Second
	options := []tlsclient.HttpClientOption{
		tlsclient.WithClientProfile(profiles.Chrome_150),
		tlsclient.WithRandomTLSExtensionOrder(),
		tlsclient.WithDisableHttp3(),
		tlsclient.WithNotFollowRedirects(),
		tlsclient.WithTimeoutSeconds(5),
		tlsclient.WithDialer(net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}),
		tlsclient.WithProxyUrl(proxy),
		tlsclient.WithCookieJar(tlsclient.NewCookieJar()),
		tlsclient.WithTransportOptions(&tlsclient.TransportOptions{
			IdleConnTimeout: &idleTimeout, MaxIdleConns: 8, MaxIdleConnsPerHost: 4,
			MaxResponseHeaderBytes: 1 << 20,
		}),
	}
	if transport.insecure {
		options = append(options, tlsclient.WithInsecureSkipVerify())
	}
	connectionClient, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(), options...)
	if err != nil {
		return nil, err
	}
	dial := connectionClient.GetDialer().DialContext
	connectionClient.CloseIdleConnections()
	options = append(options, tlsclient.WithTimeoutSeconds(0), tlsclient.WithProxyUrl(""), tlsclient.WithDialContext(func(ctx context.Context, network, target string) (net.Conn, error) {
		name, port, err := net.SplitHostPort(target)
		if err == nil && address != "" && strings.EqualFold(name, host) {
			target = net.JoinHostPort(address, port)
		}
		return dial(ctx, network, target)
	}))
	return tlsclient.NewHttpClient(tlsclient.NewNoopLogger(), options...)
}

func (transport *sourceBrowserTransport) client(request *http.Request, proxyURL, address string) (browserHTTPClient, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	key := request.URL.Scheme + "://" + strings.ToLower(request.URL.Host) + "|" + proxyURL + "|" + address
	if client := transport.clients[key]; client != nil {
		return client, nil
	}
	client, err := transport.newClient(proxyURL, request.URL.Hostname(), address)
	if err != nil {
		return nil, err
	}
	if len(transport.clients) >= 8 {
		for key, old := range transport.clients {
			old.CloseIdleConnections()
			delete(transport.clients, key)
			break
		}
	}
	transport.clients[key] = client
	return client, nil
}

func huangguoBrowserHeaders(request *http.Request) fhttp.Header {
	headers := fhttp.Header{
		"sec-ch-ua":                 {`"Chromium";v="150", "Google Chrome";v="150", "Not_A Brand";v="24"`},
		"sec-ch-ua-mobile":          {"?0"},
		"sec-ch-ua-platform":        {`"macOS"`},
		"upgrade-insecure-requests": {"1"},
		"user-agent":                {"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"},
		"accept":                    {"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8"},
		"sec-fetch-site":            {"same-origin"},
		"sec-fetch-mode":            {"navigate"},
		"sec-fetch-user":            {"?1"},
		"sec-fetch-dest":            {"document"},
		"accept-language":           {"zh-CN,zh;q=0.9"},
		fhttp.HeaderOrderKey:        {"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "upgrade-insecure-requests", "user-agent", "accept", "sec-fetch-site", "sec-fetch-mode", "sec-fetch-user", "sec-fetch-dest", "referer", "accept-encoding", "accept-language", "cookie"},
	}
	if mode := request.Header.Get("Sec-Fetch-Mode"); mode != "" && mode != "navigate" {
		headers["accept"] = []string{firstNonEmpty(request.Header.Get("Accept"), "*/*")}
		headers["sec-fetch-mode"] = []string{mode}
		headers["sec-fetch-dest"] = []string{firstNonEmpty(request.Header.Get("Sec-Fetch-Dest"), "empty")}
		delete(headers, "upgrade-insecure-requests")
		delete(headers, "sec-fetch-user")
	}
	for key, values := range request.Header {
		lower := strings.ToLower(key)
		if _, fixed := headers[lower]; fixed || lower == "host" || lower == "connection" {
			continue
		}
		headers[lower] = append([]string(nil), values...)
	}
	if referer := request.Header.Get("Referer"); referer == "" {
		headers["sec-fetch-site"] = []string{"none"}
	} else if parsed, err := url.Parse(referer); err == nil && !strings.EqualFold(parsed.Host, request.URL.Host) {
		if parsed.Scheme == request.URL.Scheme && (parsed.Hostname() == "huangguo.video" || strings.HasSuffix(parsed.Hostname(), ".huangguo.video")) && (request.URL.Hostname() == "huangguo.video" || strings.HasSuffix(request.URL.Hostname(), ".huangguo.video")) {
			headers["sec-fetch-site"] = []string{"same-site"}
		} else {
			headers["sec-fetch-site"] = []string{"cross-site"}
		}
	}
	return headers
}

func standardBrowserHeaders(headers fhttp.Header) http.Header {
	result := make(http.Header, len(headers))
	for key, values := range headers {
		result[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
	}
	return result
}

func (transport *sourceBrowserTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !transport.matches(request) {
		return transport.base.RoundTrip(request)
	}
	if request.Body != nil && request.GetBody != nil {
		defer request.Body.Close()
	}
	proxy, err := transport.router.proxy(request)
	if err != nil {
		return nil, err
	}
	proxyURL := ""
	if proxy != nil {
		proxyURL = proxy.String()
	}
	key := strings.ToLower(request.URL.Host) + "|" + proxyURL
	return transport.roundTripRoutes(request, proxyURL, key)
}

func (transport *sourceBrowserTransport) roundTripAddress(request *http.Request, proxyURL, address string) (*http.Response, error) {
	client, err := transport.client(request, proxyURL, address)
	if err != nil {
		return nil, fmt.Errorf("站源浏览器客户端初始化失败：%w", publicError(err))
	}
	body := request.Body
	if request.GetBody != nil {
		body, err = request.GetBody()
		if err != nil {
			return nil, err
		}
	}
	attemptCtx, cancel := context.WithCancel(request.Context())
	upstream, err := fhttp.NewRequestWithContext(attemptCtx, request.Method, request.URL.String(), body)
	if err != nil {
		cancel()
		if body != nil {
			body.Close()
		}
		return nil, err
	}
	upstream.Header = huangguoBrowserHeaders(request)
	if transport.userAgent != nil {
		if agent := transport.userAgent(request); agent != "" {
			upstream.Header["user-agent"] = []string{agent}
			delete(upstream.Header, "sec-ch-ua")
			delete(upstream.Header, "sec-ch-ua-mobile")
			delete(upstream.Header, "sec-ch-ua-platform")
		}
	}
	upstream.Host = request.Host
	upstream.ContentLength = request.ContentLength
	upstream.GetBody = request.GetBody
	response, err := client.Do(upstream)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		cancel()
		return nil, publicError(err)
	}
	response.Body = &limitedResponseBody{ReadCloser: response.Body, release: cancel}
	result := &http.Response{
		Status: response.Status, StatusCode: response.StatusCode,
		Proto: response.Proto, ProtoMajor: response.ProtoMajor, ProtoMinor: response.ProtoMinor,
		Header: standardBrowserHeaders(response.Header), Body: response.Body,
		ContentLength: response.ContentLength, TransferEncoding: response.TransferEncoding,
		Close: response.Close, Uncompressed: response.Uncompressed,
		Trailer: standardBrowserHeaders(response.Trailer), Request: request,
	}
	if state := response.TLS; state != nil {
		result.TLS = &tls.ConnectionState{Version: state.Version, HandshakeComplete: state.HandshakeComplete,
			DidResume: state.DidResume, CipherSuite: state.CipherSuite, NegotiatedProtocol: state.NegotiatedProtocol,
			ServerName: state.ServerName, PeerCertificates: state.PeerCertificates, VerifiedChains: state.VerifiedChains}
	}
	if transport.record != nil {
		level := "info"
		if result.StatusCode >= 400 || strings.EqualFold(result.Header.Get("Cf-Mitigated"), "challenge") {
			level = "warning"
		}
		transport.record(diagnosticEvent{Event: "network.browser_request", Level: level, Source: transport.requestSource(request),
			Host: request.URL.Hostname(), HTTPStatus: result.StatusCode, Client: huangguoBrowserProfile,
			Protocol: result.Proto, CFRay: truncate(result.Header.Get("Cf-Ray"), 128),
			ResponseType: truncate(result.Header.Get("Content-Type"), 128), Message: "站源浏览器请求完成"})
	}
	return result, nil
}

func (transport *sourceBrowserTransport) CloseIdleConnections() {
	transport.mu.Lock()
	for key, client := range transport.clients {
		client.CloseIdleConnections()
		delete(transport.clients, key)
	}
	transport.mu.Unlock()
	if closer, ok := transport.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
