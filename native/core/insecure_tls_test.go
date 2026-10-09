package core

import (
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func tlsFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func tlsFixtureDownloader(t *testing.T, browserHost string, insecure bool) *Downloader {
	t.Helper()
	directory := t.TempDir()
	downloader := &Downloader{cfg: defaultConfig(), providerHosts: map[string]string{}, proxyRouter: &proxyRouter{},
		limiter: newRequestLimiter(1, 0), diagnostics: newDiagnosticLog(directory)}
	downloader.cfg.dataDir = directory
	downloader.cfg.HuangguoVideoURL = browserHost
	downloader.cfg.InsecureTLS = insecure
	downloader.buildHTTPClient()
	t.Cleanup(downloader.client.CloseIdleConnections)
	return downloader
}

func TestInsecureTLSAppliesToProviderClients(t *testing.T) {
	server := tlsFixtureServer(t)
	builders := []struct {
		name  string
		build func(*testing.T, *httptest.Server, bool) *http.Client
	}{
		{"baseline", func(t *testing.T, server *httptest.Server, insecure bool) *http.Client {
			return tlsFixtureDownloader(t, "https://browser.example.test", insecure).client
		}},
		{"browser", func(t *testing.T, server *httptest.Server, insecure bool) *http.Client {
			return tlsFixtureDownloader(t, server.URL, insecure).client
		}},
		{"yeguo", func(t *testing.T, server *httptest.Server, insecure bool) *http.Client {
			return tlsFixtureDownloader(t, "https://browser.example.test", insecure).yeguoClient().requestClient()
		}},
	}
	for _, builder := range builders {
		for _, insecure := range []bool{true, false} {
			label := "closed"
			if insecure {
				label = "open"
			}
			t.Run(builder.name+"/"+label, func(t *testing.T) {
				client := builder.build(t, server, insecure)
				response, err := client.Get(server.URL + "/videos")
				if insecure {
					if err != nil {
						t.Fatalf("开启后自签证书请求仍失败：%v", err)
					}
					response.Body.Close()
					return
				}
				if err == nil {
					response.Body.Close()
					t.Fatal("未开启时自签证书请求应失败")
				}
				if !isCertificateSourceError(err) {
					t.Fatalf("自签证书错误未被识别：%v", err)
				}
				if !sourceTerminalError(err) {
					t.Fatal("自签证书错误应作为终端错误停止重试")
				}
			})
		}
	}
}

func TestInsecureTLSTakesEffectWithoutRebuild(t *testing.T) {
	server := tlsFixtureServer(t)
	downloader := tlsFixtureDownloader(t, server.URL, false)
	if response, err := downloader.client.Get(server.URL + "/videos"); err == nil {
		response.Body.Close()
		t.Fatal("未开启时自签证书请求应失败")
	}
	downloader.setInsecureTLS(true)
	response, err := downloader.client.Get(server.URL + "/videos")
	if err != nil {
		t.Fatalf("切换后自签证书请求失败：%v", err)
	}
	response.Body.Close()
	holder, ok := downloader.client.Transport.(*chainTransport)
	if !ok {
		t.Fatal("站源网络传输链缺失")
	}
	chain := holder.current.Load()
	if chain == nil {
		t.Fatal("站源网络传输链未初始化")
	}
	pinned := chain.cdn.pinnedTransport("huangguo.video", (*url.URL)(nil), "huangguo.video", "127.0.0.1")
	if pinned.TLSClientConfig == nil || !pinned.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("CDN 固定地址传输未继承 TLS 校验开关")
	}
}

func TestInsecureTLSToggleDuringRequestsIsRaceFree(t *testing.T) {
	server := tlsFixtureServer(t)
	server.Config.SetKeepAlivesEnabled(false)
	downloader := tlsFixtureDownloader(t, "https://browser.example.test", false)
	stop := make(chan struct{})
	var workers sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if response, err := downloader.client.Get(server.URL + "/videos"); err == nil {
					response.Body.Close()
				}
			}
		}()
	}
	for round := 0; round < 200; round++ {
		downloader.setInsecureTLS(round%2 == 0)
		time.Sleep(time.Millisecond)
	}
	close(stop)
	workers.Wait()
}

func TestSourceTaskReportsCertificateReplacement(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Get", URL: request.URL.String(), Err: x509.UnknownAuthorityError{}}
	})
	engine.catalogs[sourceHongguo] = []nativeDrama{{ID: "hongguo:1", Source: sourceHongguo, Title: "合成短剧"}}
	if _, err := engine.startSourceTask(sourceHongguo, "metadata", nativeDrama{}); err != nil {
		t.Fatal(err)
	}
	status := awaitSourceTask(t, engine, sourceHongguo)
	if status.Stage != "站点证书不受信任" {
		t.Fatalf("certificate stage = %q, error = %q", status.Stage, status.Error)
	}
	for _, hint := range []string{"本机代理", "直连", "允许本机代理替换证书（关闭 TLS 校验）"} {
		if !strings.Contains(status.Error, hint) {
			t.Fatalf("certificate hint %q missing: %q", hint, status.Error)
		}
	}
	if !status.RetryAt.IsZero() {
		t.Fatalf("certificate failure should stay terminal instead of retrying: %s", status.RetryAt)
	}
}

func TestResourceSettingsAllowInsecureTLSPersists(t *testing.T) {
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.downloads.close)
	if engine.downloader.insecureTLS() {
		t.Fatal("默认应保持 TLS 证书校验")
	}
	settings, err := engine.saveResourceSettings(nativeResourceSettings{
		ProxyMode: "auto", CatalogConcurrency: 3, DownloadConcurrency: 2, AllowInsecureTLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !settings.AllowInsecureTLS || !engine.downloader.insecureTLS() {
		t.Fatal("保存后未开启 TLS 校验跳过")
	}
	engine.downloader.setInsecureTLS(false)
	engine.loadResourceSettings()
	if !engine.downloader.insecureTLS() {
		t.Fatal("重新读取设置后未恢复 TLS 校验跳过")
	}
}
