package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSourceCatalogPageMissingClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"direct 404", errors.New("huangguo.video HTTP 404"), true},
		{"wrapped 404", fmt.Errorf("野果来源请求失败：%w", errors.New("api.yeguo.test HTTP 404")), true},
		{"url error 404", &url.Error{Op: "Get", URL: "https://huangguo.video/videos?page=56", Err: errors.New("huangguo.video HTTP 404")}, true},
		{"typed 404", &sourceTransientError{message: "api.yeguo.test HTTP 404", status: http.StatusNotFound}, true},
		{"gone", errors.New("huangguo.video HTTP 410"), true},
		{"server error typed", &sourceTransientError{message: "api.yeguo.test HTTP 503", status: http.StatusServiceUnavailable}, false},
		{"server error text", errors.New("huangguo.video HTTP 500"), false},
		{"timeout", context.DeadlineExceeded, false},
		{"request backoff", &requestBackoff{host: "huangguo.video", status: http.StatusNotFound, until: time.Now().Add(time.Minute)}, false},
	}
	for _, item := range cases {
		if got := sourceCatalogPageMissing(item.err); got != item.want {
			t.Fatalf("%s: sourceCatalogPageMissing = %v, want %v", item.name, got, item.want)
		}
	}
}

func yeguoPagingTransport(tailStatus int) sourceFixtureTransport {
	return func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "api.yeguo.test" || request.URL.Path != "/api/theater/exploreList" {
			return nil, fmt.Errorf("unexpected yeguo request: %s", request.URL.String())
		}
		if err := request.ParseForm(); err != nil {
			return nil, err
		}
		if request.Form.Get("page") != "1" {
			return sourceFixtureResponse(request, tailStatus, "page out of range"), nil
		}
		body := `{"status":"1","data":{"list":[{"video_id":"9001","title":"野果完结剧","description":"完整资料","episode_count":"6","serialize_status":"2"}],"page":1,"limit":20,"total":40,"has_more":"1"}}`
		return sourceFixtureResponse(request, http.StatusOK, body), nil
	}
}

func runYeguoUpdateTask(t *testing.T, engine *nativeEngine) {
	t.Helper()
	engine.downloader.yeguoClient().access = &yeguoAccess{base: "https://api.yeguo.test", identifier: "fixture-trace", loadedAt: time.Now()}
	engine.runSourceTask(context.Background(), sourceYeguo, "update", nativeDrama{}, &nativeSourceTask{cancel: func() {}})
}

func TestSourceContinuousUpdateEndsOnMissingTailPage(t *testing.T) {
	engine := sourceFixtureEngine(t, yeguoPagingTransport(http.StatusNotFound))
	runYeguoUpdateTask(t, engine)

	status := engine.sourceStatus(sourceYeguo)
	if status.Running || status.Stage != "已完成" || status.Error != "" {
		t.Fatalf("翻页越界 404 应正常结束：stage=%q error=%q", status.Stage, status.Error)
	}
	if status.HasMore {
		t.Fatal("翻页越界 404 后仍标记还有下一页")
	}
	cached := engine.nativeCached(sourceYeguo)
	if cached.HasMore || cached.Page != 1 || len(cached.Items) != 1 {
		t.Fatalf("目录进度或内容被破坏：%+v", cached)
	}
	restored := &nativeEngine{directory: engine.directory}
	restored.loadCatalogCache()
	if state := restored.catalogStates[sourceYeguo]; state.HasMore {
		t.Fatal("目录已加载完的状态未持久化")
	}
}

func TestSourceContinuousUpdateKeepsFirstPage404AsError(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "api.yeguo.test" || request.URL.Path != "/api/theater/exploreList" {
			return nil, fmt.Errorf("unexpected yeguo request: %s", request.URL.String())
		}
		return sourceFixtureResponse(request, http.StatusNotFound, "not found"), nil
	})
	runYeguoUpdateTask(t, engine)

	status := engine.sourceStatus(sourceYeguo)
	if status.Running || status.Stage != "未完成" || !strings.Contains(status.Error, "HTTP 404") {
		t.Fatalf("首页 404 应仍然报错：stage=%q error=%q", status.Stage, status.Error)
	}
	if !engine.nativeCached(sourceYeguo).HasMore {
		t.Fatal("首页 404 不应被当作目录已加载完")
	}
}

func TestSourceContinuousUpdateEndsOnHuangguoVideoTail404(t *testing.T) {
	cards := strings.Builder{}
	for index := 0; index < 20; index++ {
		cards.WriteString(fmt.Sprintf(`<article class="video-card"><a href="/video/synthetic-%d" title="合成短剧%d">合成短剧%d</a></article>`, index, index, index))
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/videos" && request.URL.Query().Get("page") == "1" {
			writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = writer.Write([]byte(cards.String()))
			return
		}
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte("page out of range"))
	}))
	defer server.Close()

	engine := sourceFixtureEngine(t, server.Client().Transport.(*http.Transport).RoundTrip)
	engine.downloader.cfg.HuangguoVideoURL = server.URL
	engine.runSourceTask(context.Background(), sourceHuangguoVideo, "update", nativeDrama{}, &nativeSourceTask{cancel: func() {}})

	status := engine.sourceStatus(sourceHuangguoVideo)
	if status.Running || status.Stage != "已完成" || status.Error != "" {
		t.Fatalf("黄果视频翻页越界 404 应正常结束：stage=%q error=%q", status.Stage, status.Error)
	}
	if status.HasMore {
		t.Fatal("黄果视频翻页越界 404 后仍标记还有下一页")
	}
	cached := engine.nativeCached(sourceHuangguoVideo)
	if cached.HasMore || cached.Page != 1 || len(cached.Items) != 20 {
		t.Fatalf("黄果视频目录进度或内容被破坏：page=%d hasMore=%v items=%d", cached.Page, cached.HasMore, len(cached.Items))
	}
}

func TestSourceUpdateReprobesTailPageAfterCompletion(t *testing.T) {
	previousBuild := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = previousBuild }()

	cards := strings.Builder{}
	for index := 0; index < 20; index++ {
		cards.WriteString(fmt.Sprintf(`<article class="video-card"><a href="/video/synthetic-%d" title="合成短剧%d">合成短剧%d</a></article>`, index, index, index))
	}
	var mu sync.Mutex
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/videos" {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte("not found"))
			return
		}
		page := request.URL.Query().Get("page")
		mu.Lock()
		pages = append(pages, page)
		mu.Unlock()
		if page == "1" {
			writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = writer.Write([]byte(cards.String()))
			return
		}
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte("page out of range"))
	}))
	defer server.Close()

	engine := sourceFixtureEngine(t, server.Client().Transport.(*http.Transport).RoundTrip)
	engine.downloader.cfg.HuangguoVideoURL = server.URL
	engine.mu.Lock()
	engine.catalogs[sourceHuangguoVideo] = []nativeDrama{{ID: providerDramaID(sourceHuangguoVideo, "video/synthetic-0"), Source: sourceHuangguoVideo, SourceID: "video/synthetic-0", Title: "合成短剧0"}}
	engine.catalogStates[nativeCatalogKey(sourceHuangguoVideo, "")] = nativeCatalogState{Page: 5, HasMore: false, UpdatedAt: time.Now()}
	engine.mu.Unlock()
	for round := 0; round < 2; round++ {
		if _, err := engine.startSourceTask(sourceHuangguoVideo, "update", nativeDrama{}); err != nil {
			t.Fatal(err)
		}
		status := awaitSourceTask(t, engine, sourceHuangguoVideo)
		if status.Running || status.Stage != "已完成" || status.Error != "" || status.HasMore {
			t.Fatalf("第 %d 轮更新未干净收尾：stage=%q error=%q hasMore=%v", round+1, status.Stage, status.Error, status.HasMore)
		}
	}
	mu.Lock()
	sequence := strings.Join(pages, ",")
	mu.Unlock()
	if sequence != "1,6,1,6" {
		t.Fatalf("每轮更新都应重新探测尾部页，实际 page 序列 = %s", sequence)
	}
}

func TestSourceUpdateReprobesTailPageButResumeKeepsState(t *testing.T) {
	previousBuild := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = previousBuild }()

	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusNotFound, "not found"), nil
	})
	engine.downloader.cfg.HuangguoVideoURL = "https://huangguo.video"
	engine.changeSourceRecord(sourceHuangguoVideo, func(record *nativeSourceRecord) { record.CatalogStarted = true })
	key := nativeCatalogKey(sourceHuangguoVideo, "")
	seed := func() {
		engine.mu.Lock()
		engine.catalogStates[key] = nativeCatalogState{Page: 5, HasMore: false}
		engine.mu.Unlock()
	}
	state := func() nativeCatalogState {
		engine.mu.Lock()
		defer engine.mu.Unlock()
		return engine.catalogStates[key]
	}

	seed()
	if _, err := engine.startSourceTask(sourceHuangguoVideo, "resume", nativeDrama{}); err != nil {
		t.Fatal(err)
	}
	awaitSourceTask(t, engine, sourceHuangguoVideo)
	if kept := state(); kept.HasMore || kept.Page != 5 {
		t.Fatalf("resume 不应重置分页状态：page=%d hasMore=%v", kept.Page, kept.HasMore)
	}

	seed()
	if _, err := engine.startSourceTask(sourceHuangguoVideo, "update", nativeDrama{}); err != nil {
		t.Fatal(err)
	}
	awaitSourceTask(t, engine, sourceHuangguoVideo)
	if reopened := state(); !reopened.HasMore {
		t.Fatal("update 应重新打开尾部页探测")
	}
	restored := &nativeEngine{directory: engine.directory}
	restored.loadCatalogCache()
	if persisted := restored.catalogStates[key]; !persisted.HasMore {
		t.Fatal("重新打开尾部页探测的状态未持久化")
	}
}

func TestSourceContinuousUpdateKeepsTailServerErrorAsFailure(t *testing.T) {
	previous := nativeSourceRetryAttemptLimit
	nativeSourceRetryAttemptLimit = 1
	defer func() { nativeSourceRetryAttemptLimit = previous }()
	engine := sourceFixtureEngine(t, yeguoPagingTransport(http.StatusInternalServerError))
	engine.sourceRetryPolicy.limit = time.Minute
	runYeguoUpdateTask(t, engine)

	status := engine.sourceStatus(sourceYeguo)
	if status.Running || status.Stage != "连续失败已暂停" || !status.Resumable || !strings.Contains(status.Error, "HTTP 500") {
		t.Fatalf("5xx 不应被当作目录结束：stage=%q error=%q", status.Stage, status.Error)
	}
	if !engine.nativeCached(sourceYeguo).HasMore {
		t.Fatal("5xx 不应被当作目录已加载完")
	}
}
