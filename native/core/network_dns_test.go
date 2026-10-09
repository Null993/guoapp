package core

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func dohFixtureResolver(t *testing.T, server *httptest.Server, insecure bool, serverName string) *dnsResolver {
	t.Helper()
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	base.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure, ServerName: serverName}
	resolver := newDNSResolver(base)
	resolver.endpoints = []string{server.URL + "/resolve"}
	return resolver
}

func dohFixtureServer(t *testing.T, seen func(*http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seen(request)
		writer.Header().Set("Content-Type", "application/dns-json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"Status": 0, "Answer": []map[string]any{{"type": 1, "TTL": 120, "data": "93.184.216.34"}}})
	}))
}

func TestDNSResolverInheritsBaseTLSConfig(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	base.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, ServerName: "pinned.tuafjz.cn"}
	resolver := newDNSResolver(base)
	lookupTransport, ok := resolver.client.Transport.(*http.Transport)
	if !ok || lookupTransport.TLSClientConfig == nil {
		t.Fatal("DoH lookup transport lost the base TLS configuration")
	}
	if !lookupTransport.TLSClientConfig.InsecureSkipVerify || lookupTransport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("DoH lookup transport did not inherit the base TLS configuration: %+v", lookupTransport.TLSClientConfig)
	}
	if lookupTransport.TLSClientConfig.ServerName != "" {
		t.Fatalf("DoH lookup transport inherited a pinned ServerName: %q", lookupTransport.TLSClientConfig.ServerName)
	}
	if base.TLSClientConfig.ServerName != "pinned.tuafjz.cn" || !base.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("building the DoH resolver mutated the base TLS configuration")
	}
	plainBase := http.DefaultTransport.(*http.Transport).Clone()
	plainBase.TLSClientConfig = nil
	plain := newDNSResolver(plainBase)
	if config := plain.client.Transport.(*http.Transport).TLSClientConfig; config != nil {
		t.Fatalf("DoH resolver invented a TLS configuration without a base one: %+v", config)
	}
}

func TestDNSResolverDoHRespectsInsecureTLS(t *testing.T) {
	var queries []string
	server := dohFixtureServer(t, func(request *http.Request) {
		queries = append(queries, request.URL.Query().Get("name")+"|"+request.URL.Query().Get("type"))
		if request.TLS != nil && request.TLS.ServerName == "pinned.tuafjz.cn" {
			t.Error("DoH request reused the pinned ServerName as SNI")
		}
	})
	defer server.Close()

	strict := dohFixtureResolver(t, server, false, "")
	if _, err := strict.lookup(context.Background(), "protected.example.test"); err == nil {
		t.Fatal("self-signed DoH endpoint was trusted without InsecureSkipVerify")
	} else if !strings.Contains(err.Error(), "x509") {
		t.Fatalf("strict DoH failure was not a certificate error: %v", err)
	}

	relaxed := dohFixtureResolver(t, server, true, "pinned.tuafjz.cn")
	entry, err := relaxed.lookupEntry(context.Background(), "protected.example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entry.addresses, []string{"93.184.216.34"}) || !entry.expires.After(time.Now()) {
		t.Fatalf("DoH answer over the insecure transport was not usable: %+v", entry)
	}
	if len(queries) != 1 || queries[0] != "protected.example.test|A" {
		t.Fatalf("unexpected DoH queries: %v", queries)
	}
}

func TestInsecureTLSToggleRebuildsNetworkChain(t *testing.T) {
	downloader := tlsFixtureDownloader(t, "https://browser.example.test", false)
	previous := downloader.chain.current.Load()
	if previous == nil || previous.base.TLSClientConfig == nil || previous.base.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("初始链路不应跳过证书校验")
	}
	downloader.setInsecureTLS(true)
	chain := downloader.chain.current.Load()
	if chain == nil || chain == previous {
		t.Fatal("切换后未重建站源网络链路")
	}
	if !chain.base.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("新链路未继承 TLS 校验开关")
	}
	lookup := chain.resolver.client.Transport.(*http.Transport)
	if lookup.TLSClientConfig == nil || !lookup.TLSClientConfig.InsecureSkipVerify || lookup.TLSClientConfig.ServerName != "" {
		t.Fatalf("新 DoH 传输未继承新的 TLS 配置：%+v", lookup.TLSClientConfig)
	}
	if previous.base.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("切换就地修改了旧链路，仍会与在途请求竞争")
	}
	pinned := chain.cdn.pinnedTransport("huangguo.video", nil, "huangguo.video", "127.0.0.1")
	if pinned.TLSClientConfig == nil || !pinned.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("新 CDN 固定地址传输未继承 TLS 开关")
	}
}

func TestInsecureTLSToggleReachesDoHResolver(t *testing.T) {
	server := dohFixtureServer(t, func(request *http.Request) {})
	defer server.Close()
	downloader := tlsFixtureDownloader(t, "https://browser.example.test", false)
	resolver := downloader.currentDNSResolver()
	resolver.endpoints = []string{server.URL + "/resolve"}

	if _, err := resolver.lookup(context.Background(), "strict.example.test"); err == nil {
		t.Fatal("关闭校验时自签 DoH 端点不应被信任")
	}
	downloader.setInsecureTLS(true)
	refreshed := downloader.currentDNSResolver()
	if refreshed == resolver {
		t.Fatal("切换后 DoH 解析器未随链路重建")
	}
	if _, err := resolver.lookup(context.Background(), "stale.example.test"); err == nil {
		t.Fatal("旧 DoH 解析器被就地改成跳过校验")
	}
	refreshed.endpoints = []string{server.URL + "/resolve"}
	entry, err := refreshed.lookupEntry(context.Background(), "relaxed.example.test", "")
	if err != nil {
		t.Fatalf("运行时开启后 DoH 未继承 TLS 开关：%v", err)
	}
	if !reflect.DeepEqual(entry.addresses, []string{"93.184.216.34"}) {
		t.Fatalf("运行时开启后 DoH 结果不正确：%+v", entry)
	}
}

func TestInsecureTLSToggleCoversStartupOrders(t *testing.T) {
	server := dohFixtureServer(t, func(request *http.Request) {})
	defer server.Close()

	directory := t.TempDir()
	engine, err := newNativeEngine(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.downloads.close)
	if _, err := engine.saveResourceSettings(nativeResourceSettings{
		ProxyMode: "auto", CatalogConcurrency: 3, DownloadConcurrency: 2, AllowInsecureTLS: true,
	}); err != nil {
		t.Fatal(err)
	}
	restarted, err := newNativeEngine(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.downloads.close)
	resolver := restarted.downloader.currentDNSResolver()
	resolver.endpoints = []string{server.URL + "/resolve"}
	if _, err := resolver.lookupEntry(context.Background(), "startup.example.test", ""); err != nil {
		t.Fatalf("启动装载设置后 DoH 未继承 TLS 开关：%v", err)
	}

	early := tlsFixtureDownloader(t, "https://browser.example.test", true)
	earlyResolver := early.currentDNSResolver()
	earlyResolver.endpoints = []string{server.URL + "/resolve"}
	if _, err := earlyResolver.lookupEntry(context.Background(), "early.example.test", ""); err != nil {
		t.Fatalf("先置 true 再建链路的路径未继承：%v", err)
	}
}
