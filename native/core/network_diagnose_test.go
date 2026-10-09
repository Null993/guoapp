package core

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProxyInterceptedResponseClassification(t *testing.T) {
	empty := http.Header{}
	withServer := http.Header{"Server": {"nginx/1.24.0"}}
	cases := []struct {
		name    string
		status  int
		header  http.Header
		body    []byte
		elapsed time.Duration
		want    bool
	}{
		{"6ms empty 403 no server", http.StatusForbidden, empty, nil, 6 * time.Millisecond, true},
		{"6ms empty 404 no server", http.StatusNotFound, empty, nil, 6 * time.Millisecond, true},
		{"6ms empty 407 no server", http.StatusProxyAuthRequired, empty, nil, 6 * time.Millisecond, true},
		{"6ms empty 451 no server", http.StatusUnavailableForLegalReasons, empty, nil, 6 * time.Millisecond, true},
		{"660ms nginx 404 with body", http.StatusNotFound, withServer, []byte("<html>404</html>"), 660 * time.Millisecond, false},
		{"slow empty 404 no server", http.StatusNotFound, empty, nil, 400 * time.Millisecond, false},
		{"empty 404 with server header", http.StatusNotFound, withServer, nil, 6 * time.Millisecond, false},
		{"empty 404 with body", http.StatusNotFound, empty, []byte("x"), 6 * time.Millisecond, false},
		{"6ms empty 500", http.StatusInternalServerError, empty, nil, 6 * time.Millisecond, false},
		{"6ms empty 200", http.StatusOK, empty, nil, 6 * time.Millisecond, false},
		{"unknown timing keeps primary signals", http.StatusNotFound, empty, nil, 0, false},
		{"negative timing", http.StatusNotFound, empty, nil, -time.Millisecond, false},
		{"exactly window boundary", http.StatusNotFound, empty, nil, proxyInterceptionWindow, false},
	}
	for _, item := range cases {
		if got := proxyInterceptedResponse(item.status, item.header, item.body, item.elapsed); got != item.want {
			t.Fatalf("%s: proxyInterceptedResponse = %v, want %v", item.name, got, item.want)
		}
	}
}

func TestProxyInterceptedHostSkipsLocalTargets(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"cdn.huangguo.video", true},
		{"huangguo.video", true},
		{"cdn.huangguo.video:443", true},
		{"127.0.0.1", false},
		{"127.0.0.1:7890", false},
		{"localhost", false},
		{"localhost:7890", false},
		{"[::1]", false},
		{"[::1]:7890", false},
		{"192.168.1.10", false},
		{"192.168.1.10:8080", false},
		{"10.0.0.5", false},
		{"172.16.8.9", false},
		{"nas", false},
		{"printer.local", false},
		{"", false},
	}
	for _, item := range cases {
		if got := proxyInterceptedHost(item.host); got != item.want {
			t.Fatalf("proxyInterceptedHost(%q) = %v, want %v", item.host, got, item.want)
		}
	}
	plain := describeHTTPFailure("127.0.0.1", http.StatusNotFound, http.Header{}, nil, time.Millisecond)
	if plain.Error() != "127.0.0.1 HTTP 404" {
		t.Fatalf("本机目标不应被判为代理拦截：%v", plain)
	}
}

func TestSourceProbeReportsProxyInterception(t *testing.T) {
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusNotFound, ""), nil
	})
	ctx := context.Background()
	if _, err := downloader.sourceProbeBytes(ctx, "https://cdn.huangguo.video/e37acc9a.ts", "https://huangguo.video/", false); err == nil {
		t.Fatal("瞬时空 404 应返回错误")
	} else {
		message := publicError(err).Error()
		for _, hint := range []string{"疑似本机代理拦截", "空 404", "无 Server 头", "ms", "改用直连"} {
			if !strings.Contains(message, hint) {
				t.Fatalf("错误文案缺少 %q：%q", hint, message)
			}
		}
		var intercepted *proxyInterceptionError
		if !errors.As(err, &intercepted) || intercepted.status != http.StatusNotFound || intercepted.host != "cdn.huangguo.video" {
			t.Fatalf("错误类型或字段不正确：%#v", intercepted)
		}
	}
}

func TestSourceHealthStepMarksProxyInterception(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/videos":
			return sourceFixtureResponse(request, http.StatusOK, `<article class="video-card"><a href="/video/fixture" title="合成检测剧">合成检测剧</a></article>`), nil
		case "/video/fixture":
			return sourceFixtureResponse(request, http.StatusOK, `<h1>合成检测剧</h1><div data-hls="https://huangguo.video/master.m3u8"></div>`), nil
		case "/master.m3u8":
			return sourceFixtureResponse(request, http.StatusOK, "#EXTM3U\n#EXT-X-STREAM-INF:RESOLUTION=320x480\nmedia.m3u8?signature=a%2Fb\n"), nil
		case "/media.m3u8":
			return sourceFixtureResponse(request, http.StatusOK, "#EXTM3U\n#EXTINF:4,\nsegment.ts\n#EXT-X-ENDLIST"), nil
		default:
			return sourceFixtureResponse(request, http.StatusNotFound, ""), nil
		}
	})
	engine.downloader.cfg.HuangguoVideoURL = "https://huangguo.video"
	if err := engine.checkSource(context.Background(), sourceHuangguoVideo, nativeDrama{}, true); err == nil {
		t.Fatal("媒体分片被拦截时健康检测应失败")
	}
	health := engine.sourceStatus(sourceHuangguoVideo).Health
	if health == nil || health.State != "failed" {
		t.Fatalf("健康报告缺失：%+v", health)
	}
	var media *nativeHealthStep
	for index := range health.Steps {
		if health.Steps[index].Name == "媒体连接" {
			media = &health.Steps[index]
		}
	}
	if media == nil || media.State != "failed" {
		t.Fatalf("媒体连接步骤缺失：%+v", health.Steps)
	}
	if !media.ProxyIntercepted || media.HTTPStatus != http.StatusNotFound {
		t.Fatalf("媒体连接步骤未标记代理拦截：%+v", media)
	}
	if !strings.Contains(media.Message, "疑似本机代理拦截") {
		t.Fatalf("媒体连接步骤详情不完整：%+v", media)
	}
}

func TestProxyInterceptionIsNotCatalogTail(t *testing.T) {
	intercepted := describeHTTPFailure("cdn.huangguo.video", http.StatusNotFound, http.Header{}, nil, 6*time.Millisecond)
	if sourceCatalogPageMissing(intercepted) {
		t.Fatal("疑似代理拦截不应被 sourceCatalogPageMissing 判为页尾")
	}
	realTail := describeHTTPFailure("huangguo.video", http.StatusNotFound, http.Header{"Server": {"nginx/1.24.0"}}, []byte("<html>404</html>"), 660*time.Millisecond)
	if !sourceCatalogPageMissing(realTail) {
		t.Fatalf("真实 404 仍应判为页尾：%v", realTail)
	}

	previousBuild := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = previousBuild }()

	cards := strings.Builder{}
	for index := 0; index < 20; index++ {
		fmt.Fprintf(&cards, `<article class="video-card"><a href="/video/synthetic-%d" title="合成短剧%d">合成短剧%d</a></article>`, index, index, index)
	}
	var mu sync.Mutex
	var pages []string
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/videos" {
			return sourceFixtureResponse(request, http.StatusNotFound, ""), nil
		}
		page := request.URL.Query().Get("page")
		mu.Lock()
		pages = append(pages, page)
		mu.Unlock()
		if page == "1" {
			return sourceFixtureResponse(request, http.StatusOK, cards.String()), nil
		}
		return sourceFixtureResponse(request, http.StatusNotFound, ""), nil
	})
	engine.downloader.cfg.HuangguoVideoURL = "https://huangguo.video"
	engine.runSourceTask(context.Background(), sourceHuangguoVideo, "update", nativeDrama{}, &nativeSourceTask{cancel: func() {}})

	status := engine.sourceStatus(sourceHuangguoVideo)
	if status.Stage != "未完成" || !strings.Contains(status.Error, "疑似本机代理拦截") {
		t.Fatalf("页尾被代理拦截时应报错：stage=%q error=%q", status.Stage, status.Error)
	}
	if !engine.nativeCached(sourceHuangguoVideo).HasMore {
		t.Fatal("疑似代理拦截不应被当作目录已加载完")
	}
	mu.Lock()
	sequence := strings.Join(pages, ",")
	mu.Unlock()
	if sequence != "1,2" {
		t.Fatalf("page 序列 = %s，期望 1,2", sequence)
	}
}

func metadataDetailTransport(detail func(*http.Request) (*http.Response, error)) sourceFixtureTransport {
	return func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/videos" {
			return sourceFixtureResponse(request, http.StatusOK, `<article class="video-card"><a href="/video/synthetic-0" title="合成短剧0">合成短剧0</a></article>`), nil
		}
		return detail(request)
	}
}

func TestSourceMetadataKeepsInterceptionPending(t *testing.T) {
	previousBuild := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = previousBuild }()

	engine := sourceFixtureEngine(t, metadataDetailTransport(func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusNotFound, ""), nil
	}))
	engine.downloader.cfg.HuangguoVideoURL = "https://huangguo.video"
	engine.runSourceTask(context.Background(), sourceHuangguoVideo, "update", nativeDrama{}, &nativeSourceTask{cancel: func() {}})

	status := engine.sourceStatus(sourceHuangguoVideo)
	if status.Stage != "未完成" || !strings.Contains(status.Error, "疑似本机代理拦截") {
		t.Fatalf("资料阶段遇到代理拦截应中断并给出可操作文案：stage=%q error=%q", status.Stage, status.Error)
	}
	items := engine.nativeCached(sourceHuangguoVideo).Items
	if len(items) != 1 {
		t.Fatalf("目录条目数 = %d，期望 1", len(items))
	}
	engine.mu.Lock()
	record := engine.sourceRecords[sourceHuangguoVideo]
	checked, retryAt := record.MetadataChecked[items[0].ID], record.MetadataRetryAt[items[0].ID]
	engine.mu.Unlock()
	if !checked.IsZero() {
		t.Fatal("代理拦截不应把该剧标记为已检查（会被跳过 24 小时）")
	}
	if retryAt.IsZero() || time.Until(retryAt) > 6*time.Minute {
		t.Fatalf("代理拦截应保留待补状态（约 5 分钟重试），实际 %s", retryAt)
	}
}

func TestSourceMetadataKeepsReal404Skipped(t *testing.T) {
	previousBuild := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = previousBuild }()

	engine := sourceFixtureEngine(t, metadataDetailTransport(func(request *http.Request) (*http.Response, error) {
		response := sourceFixtureResponse(request, http.StatusNotFound, "<html>404</html>")
		response.Header.Set("Server", "nginx/1.24.0")
		return response, nil
	}))
	engine.downloader.cfg.HuangguoVideoURL = "https://huangguo.video"
	engine.runSourceTask(context.Background(), sourceHuangguoVideo, "update", nativeDrama{}, &nativeSourceTask{cancel: func() {}})

	status := engine.sourceStatus(sourceHuangguoVideo)
	if status.Stage != "已完成" || status.Error != "" {
		t.Fatalf("真实 404 应保持原语义（跳过并完成）：stage=%q error=%q", status.Stage, status.Error)
	}
	items := engine.nativeCached(sourceHuangguoVideo).Items
	if len(items) != 1 {
		t.Fatalf("目录条目数 = %d，期望 1", len(items))
	}
	engine.mu.Lock()
	record := engine.sourceRecords[sourceHuangguoVideo]
	checked, retryAt := record.MetadataChecked[items[0].ID], record.MetadataRetryAt[items[0].ID]
	engine.mu.Unlock()
	if checked.IsZero() {
		t.Fatal("真实 404 应把该剧标记为已检查")
	}
	if until := time.Until(retryAt); until < 23*time.Hour {
		t.Fatalf("真实 404 的重试间隔应为 24 小时，实际 %s", until)
	}
}

func TestHuangguoProviderTextReportsPrimaryCandidateFailure(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		requests++
		primary := requests == 1
		mu.Unlock()
		if primary {
			return sourceFixtureResponse(request, http.StatusNotFound, ""), nil
		}
		return nil, &url.Error{Op: "Get", URL: request.URL.String(), Err: x509.UnknownAuthorityError{}}
	})
	_, err := downloader.fetchProviderText(context.Background(), "https://huangguoai.com/detail/synthetic/", "https://huangguoai.com/")
	if err == nil {
		t.Fatal("所有候选都失败时应返回错误")
	}
	message := err.Error()
	for _, hint := range []string{"主候选", "疑似本机代理拦截", "最后错误", "certificate signed by unknown authority"} {
		if !strings.Contains(message, hint) {
			t.Fatalf("文案缺少 %q：%q", hint, message)
		}
	}
	var unknownAuthority x509.UnknownAuthorityError
	if !errors.As(err, &unknownAuthority) {
		t.Fatalf("末候选错误类型链断了：%v", err)
	}
}

type failingBodyReader struct{}

func (failingBodyReader) Read([]byte) (int, error) { return 0, errors.New("synthetic body failure") }

func (failingBodyReader) Close() error { return nil }

func TestHuangguoProviderTextKeepsOldFormatWhenCandidatesAgree(t *testing.T) {
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		response := sourceFixtureResponse(request, http.StatusOK, "")
		response.Body = failingBodyReader{}
		return response, nil
	})
	_, err := downloader.fetchProviderText(context.Background(), "https://huangguoai.com/detail/synthetic/", "https://huangguoai.com/")
	if err == nil {
		t.Fatal("所有候选都失败时应返回错误")
	}
	message := err.Error()
	if !strings.Contains(message, "已尝试") || !strings.Contains(message, "最后错误") {
		t.Fatalf("多候选失败应保留旧格式：%q", message)
	}
	if strings.Contains(message, "主候选") {
		t.Fatalf("主候选与最后错误相同时不应重复输出：%q", message)
	}
}

func TestHuangguoProviderTextKeepsInterceptionTypeChain(t *testing.T) {
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusNotFound, ""), nil
	})
	_, err := downloader.fetchProviderText(context.Background(), "https://huangguoai.com/detail/synthetic/", "https://huangguoai.com/")
	if err == nil {
		t.Fatal("所有候选都失败时应返回错误")
	}
	var intercepted *proxyInterceptionError
	if !errors.As(err, &intercepted) {
		t.Fatalf("末候选的拦截错误类型链断了：%v", err)
	}
	if !strings.Contains(err.Error(), "疑似本机代理拦截") || !strings.Contains(err.Error(), "主候选") {
		t.Fatalf("多候选拦截文案不完整：%q", err)
	}
}

func TestHuangguoProviderTextSingleCandidateKeepsRawError(t *testing.T) {
	downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusNotFound, ""), nil
	})
	_, err := downloader.fetchProviderText(context.Background(), "https://huangguo.video/videos", "https://huangguo.video/")
	if err == nil {
		t.Fatal("单一候选失败应返回错误")
	}
	if strings.Contains(err.Error(), "已尝试") {
		t.Fatalf("单一候选不应加多域名包装：%q", err)
	}
	if !strings.Contains(err.Error(), "疑似本机代理拦截") {
		t.Fatalf("单一候选应保留拦截文案：%q", err)
	}
}

func diagnoseRequest(t *testing.T, timed bool) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, "https://cdn.huangguo.video/e37acc9a.ts", nil)
	if err != nil {
		t.Fatal(err)
	}
	if timed {
		request = request.WithContext(requestTimingContext(request.Context()))
	}
	return request
}

func TestCatalogResponseInterceptionKeepsHostBackoff(t *testing.T) {
	downloader := sourceFixtureDownloader(t, nil)
	request := diagnoseRequest(t, true)
	response := sourceFixtureResponse(request, http.StatusForbidden, "")
	var intercepted *proxyInterceptionError
	err := downloader.catalogResponseError(request, response, nil)
	if !errors.As(err, &intercepted) {
		t.Fatalf("空 403 应给出拦截文案：%v", err)
	}
	downloader.limiter.mu.Lock()
	backoff := downloader.limiter.backoffs["cdn.huangguo.video"]
	downloader.limiter.mu.Unlock()
	if backoff == nil {
		t.Fatal("疑似拦截的 403 仍应记录 host backoff（跨任务退避语义不能丢）")
	}
	again := downloader.catalogResponseError(request, sourceFixtureResponse(request, http.StatusForbidden, ""), nil)
	if !errors.As(again, &intercepted) {
		t.Fatalf("退避窗口内仍应给出拦截文案：%v", again)
	}
	withBody := sourceFixtureResponse(request, http.StatusForbidden, "<html>403</html>")
	withBody.Header.Set("Server", "cloudflare")
	plain := downloader.catalogResponseError(request, withBody, []byte("<html>403</html>"))
	if errors.As(plain, &intercepted) {
		t.Fatalf("真实 403 不应判为拦截：%v", plain)
	}
	var backoffErr *requestBackoff
	if !errors.As(plain, &backoffErr) {
		t.Fatalf("真实 403 应保留站点拒绝退避：%v", plain)
	}
}

func TestCatalogResponseWithoutTimingIsNotInterception(t *testing.T) {
	downloader := sourceFixtureDownloader(t, nil)
	request := diagnoseRequest(t, false)
	response := sourceFixtureResponse(request, http.StatusNotFound, "")
	err := downloader.catalogResponseError(request, response, nil)
	if err == nil {
		t.Fatal("空 404 应返回错误")
	}
	if err.Error() != "cdn.huangguo.video HTTP 404" {
		t.Fatalf("媒体路径（无计时）不应判为代理拦截：%q", err.Error())
	}
	if elapsed := requestElapsed(request.Context()); elapsed != 0 {
		t.Fatalf("未计时的请求应报告 0，实际 %s", elapsed)
	}
	if elapsed := requestElapsed(diagnoseRequest(t, true).Context()); elapsed <= 0 {
		t.Fatalf("已计时的请求耗时必须为正（0 专用于未计时），实际 %s", elapsed)
	}
}

func TestSourceMetadataMissingIgnoresWrappedInterceptionText(t *testing.T) {
	intercepted := describeHTTPFailure("cdn.huangguo.video", http.StatusNotFound, http.Header{}, nil, 6*time.Millisecond)
	wrapped := fmt.Errorf("黄果来源请求失败：已尝试 14 个域名；主候选 %s；最后错误：%w", intercepted.Error(), errors.New("x509: certificate signed by unknown authority"))
	if sourceMetadataMissing(wrapped) {
		t.Fatalf("主候选拦截文案被 %%s 内联后不应判为资料不存在：%v", wrapped)
	}
	if !sourceMetadataMissing(errors.New("api.yeguo.test HTTP 404")) {
		t.Fatal("真实 HTTP 404 仍应判为资料不存在")
	}
}
