package core

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type sourceThroughputStub struct {
	mu           sync.Mutex
	items        int
	detailDelay  time.Duration
	catalogCalls int
	detailCalls  int
	inFlight     int
	peakInFlight int
	breakOnce    sync.Once
	breakWrites  func()
}

func (stub *sourceThroughputStub) snapshot() (catalog, detail, peak int) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return stub.catalogCalls, stub.detailCalls, stub.peakInFlight
}

func (stub *sourceThroughputStub) handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/videos":
			stub.mu.Lock()
			stub.catalogCalls++
			page := request.URL.Query().Get("page")
			stub.mu.Unlock()
			if page != "1" {
				writer.WriteHeader(http.StatusNotFound)
				return
			}
			writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			cards := strings.Builder{}
			for index := 0; index < stub.items; index++ {
				fmt.Fprintf(&cards, `<article class="video-card"><a href="/video/synthetic-%d" title="合成短剧%d">合成短剧%d</a></article>`, index, index, index)
			}
			_, _ = writer.Write([]byte(cards.String()))
		case strings.HasPrefix(request.URL.Path, "/video/"):
			stub.mu.Lock()
			stub.detailCalls++
			stub.inFlight++
			if stub.inFlight > stub.peakInFlight {
				stub.peakInFlight = stub.inFlight
			}
			stub.mu.Unlock()
			stub.breakOnce.Do(func() {
				if stub.breakWrites != nil {
					stub.breakWrites()
				}
			})
			if stub.detailDelay > 0 {
				time.Sleep(stub.detailDelay)
			}
			writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = writer.Write([]byte(`<h1>合成短剧</h1><meta name="description" content="完整资料"><div data-hls="https://media.example.test/master.m3u8"></div>`))
			stub.mu.Lock()
			stub.inFlight--
			stub.mu.Unlock()
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	})
}

func sourceThroughputTransport(server *httptest.Server) sourceFixtureTransport {
	transport := server.Client().Transport.(*http.Transport)
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}
	return transport.RoundTrip
}

func sourceThroughputEngine(t *testing.T, stub *sourceThroughputStub) (*nativeEngine, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(stub.handler())
	t.Cleanup(server.Close)
	engine := sourceFixtureEngine(t, sourceThroughputTransport(server))
	engine.downloader.cfg.HuangguoVideoURL = server.URL
	return engine, server
}

type sourceThroughputRun struct {
	items        int
	elapsed      time.Duration
	catalogCalls int
	detailCalls  int
	peakInFlight int
	flushes      int
	diskWrites   int
	catalogsSize int64
}

func (run sourceThroughputRun) String() string {
	return fmt.Sprintf("items=%d elapsed=%s perItem=%s catalogRequests=%d detailRequests=%d peakInFlight=%d batchFlushes=%d catalogsDiskWrites=%d catalogsBytes=%d",
		run.items, run.elapsed.Round(time.Millisecond), (run.elapsed / time.Duration(max(1, run.items))).Round(100*time.Microsecond),
		run.catalogCalls, run.detailCalls, run.peakInFlight, run.flushes, run.diskWrites, run.catalogsSize)
}

func runSourceMetadataThroughput(t *testing.T, items int, delay time.Duration, workers, flushItems int, interval time.Duration, deferDetail bool) sourceThroughputRun {
	t.Helper()
	previousWorkers, previousFlush, previousDefer := nativeMetadataWorkers, nativeMetadataFlushItems, nativeMetadataDeferDetailWrite
	nativeMetadataWorkers, nativeMetadataFlushItems, nativeMetadataDeferDetailWrite = workers, flushItems, deferDetail
	defer func() {
		nativeMetadataWorkers, nativeMetadataFlushItems, nativeMetadataDeferDetailWrite = previousWorkers, previousFlush, previousDefer
	}()

	stub := &sourceThroughputStub{items: items, detailDelay: delay}
	engine, _ := sourceThroughputEngine(t, stub)
	if interval > 0 {
		engine.downloader.limiter = newRequestLimiter(3, interval)
	}

	started := time.Now()
	engine.runSourceTask(context.Background(), sourceHuangguoVideo, "update", nativeDrama{}, &nativeSourceTask{cancel: func() {}})
	run := sourceThroughputRun{elapsed: time.Since(started), items: items}
	run.catalogCalls, run.detailCalls, run.peakInFlight = stub.snapshot()
	engine.mu.Lock()
	run.flushes = engine.sourceRecords[sourceHuangguoVideo].catalogFlushCount
	run.diskWrites = engine.catalogDiskWrites
	engine.mu.Unlock()
	if info, err := os.Stat(filepath.Join(engine.directory, "catalogs.json")); err == nil {
		run.catalogsSize = info.Size()
	}

	cached := engine.nativeCached(sourceHuangguoVideo)
	status := engine.sourceStatus(sourceHuangguoVideo)
	if status.Error != "" {
		t.Fatalf("资料补齐失败：%q", status.Error)
	}
	if len(cached.Items) != items {
		t.Fatalf("目录条目数 = %d，期望 %d", len(cached.Items), items)
	}
	return run
}

func TestSourceMetadataThroughputProbe(t *testing.T) {
	for _, delay := range []time.Duration{0, 10 * time.Millisecond} {
		delay := delay
		items := 200
		if delay > 0 {
			items = 100
		}
		t.Run(delay.String(), func(t *testing.T) {
			perItem := runSourceMetadataThroughput(t, items, delay, 1, 1, 0, false)
			batched := runSourceMetadataThroughput(t, items, delay, 4, 20, 0, false)
			deferred := runSourceMetadataThroughput(t, items, delay, 4, 20, 0, true)
			t.Logf("perItem  %s", perItem)
			t.Logf("batched  %s", batched)
			t.Logf("deferred %s", deferred)
			if batched.peakInFlight <= perItem.peakInFlight {
				t.Fatalf("资料阶段未并发：perItem peak=%d batched peak=%d", perItem.peakInFlight, batched.peakInFlight)
			}
			if batched.diskWrites >= perItem.diskWrites {
				t.Fatalf("进度落盘未批量：perItem=%d batched=%d", perItem.diskWrites, batched.diskWrites)
			}
			if deferred.diskWrites >= batched.diskWrites {
				t.Fatalf("详情落盘未转为批量：batched=%d deferred=%d", batched.diskWrites, deferred.diskWrites)
			}
			if deferred.elapsed >= batched.elapsed {
				t.Fatalf("详情落盘批量后未变快：batched=%s deferred=%s", batched.elapsed, deferred.elapsed)
			}
		})
	}
}

func TestSourceMetadataThroughputWithRateInterval(t *testing.T) {
	serial := runSourceMetadataThroughput(t, 40, 80*time.Millisecond, 1, 1, 25*time.Millisecond, false)
	parallel := runSourceMetadataThroughput(t, 40, 80*time.Millisecond, 4, 20, 25*time.Millisecond, true)
	t.Logf("serial   %s", serial)
	t.Logf("parallel %s", parallel)
	if parallel.peakInFlight <= 1 {
		t.Fatalf("限速间隔下的资料阶段未并发：peak=%d", parallel.peakInFlight)
	}
	if parallel.elapsed >= serial.elapsed {
		t.Fatalf("限速间隔下并发没有变快：serial=%s parallel=%s", serial.elapsed, parallel.elapsed)
	}
}

func TestSourceMetadataCancelKeepsFlushedProgress(t *testing.T) {
	const items = 200
	stub := &sourceThroughputStub{items: items, detailDelay: 5 * time.Millisecond}
	engine, server := sourceThroughputEngine(t, stub)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		engine.runSourceTask(ctx, sourceHuangguoVideo, "update", nativeDrama{}, &nativeSourceTask{cancel: cancel})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, calls, _ := stub.snapshot(); calls >= 8 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-finished

	status := engine.sourceStatus(sourceHuangguoVideo)
	if status.Running || status.Stage != "已停止" || status.Error != "已保留更新内容，可稍后继续" {
		t.Fatalf("取消语义变化：stage=%q error=%q running=%v", status.Stage, status.Error, status.Running)
	}
	_, cancelledAfter, _ := stub.snapshot()
	if cancelledAfter >= items {
		t.Fatalf("取消得太晚，无法验证部分进度：%d", cancelledAfter)
	}

	restored, err := newNativeEngine(engine.directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restored.downloads.close)
	restored.downloader.limiter = newRequestLimiter(3, 0)
	restored.downloader.client.Transport = sourceThroughputTransport(server)
	restored.downloader.cfg.HuangguoVideoURL = server.URL
	restored.runSourceTask(context.Background(), sourceHuangguoVideo, "update", nativeDrama{}, &nativeSourceTask{cancel: func() {}})
	_, detailAfter, _ := stub.snapshot()
	if remaining := detailAfter - cancelledAfter; remaining <= 0 || remaining >= items {
		t.Fatalf("取消后已完成的资料未被保留：续跑重新抓取了 %d 部（共 %d）", remaining, items)
	}
}

func TestSourceMetadataProgressFlushedAtTaskEnd(t *testing.T) {
	const items = 60
	stub := &sourceThroughputStub{items: items, detailDelay: time.Millisecond}
	engine, server := sourceThroughputEngine(t, stub)
	engine.runSourceTask(context.Background(), sourceHuangguoVideo, "update", nativeDrama{}, &nativeSourceTask{cancel: func() {}})

	engine.mu.Lock()
	record := engine.sourceRecords[sourceHuangguoVideo]
	flushes, cursor, checked := record.catalogFlushCount, record.MetadataCursor, len(record.MetadataChecked)
	engine.mu.Unlock()
	if checked != items {
		t.Fatalf("任务结束时 MetadataChecked = %d，期望 %d", checked, items)
	}
	if cursor == "" {
		t.Fatal("任务结束时 MetadataCursor 为空")
	}
	if flushes == 0 || flushes >= items {
		t.Fatalf("整份 catalogs.json 落盘未批量：flushes=%d items=%d", flushes, items)
	}

	_, detailBefore, _ := stub.snapshot()
	restored, err := newNativeEngine(engine.directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restored.downloads.close)
	restored.downloader.limiter = newRequestLimiter(3, 0)
	restored.downloader.client.Transport = sourceThroughputTransport(server)
	restored.downloader.cfg.HuangguoVideoURL = server.URL
	restored.runSourceTask(context.Background(), sourceHuangguoVideo, "update", nativeDrama{}, &nativeSourceTask{cancel: func() {}})
	_, detailAfter, _ := stub.snapshot()
	if detailAfter != detailBefore {
		t.Fatalf("重载后重新抓取了 %d 部资料，进度未完整落盘", detailAfter-detailBefore)
	}
	cached := restored.nativeCached(sourceHuangguoVideo)
	if len(cached.Items) != items {
		t.Fatalf("重载后目录条目数 = %d，期望 %d", len(cached.Items), items)
	}
	for _, drama := range cached.Items {
		if drama.Episodes <= 0 {
			t.Fatalf("重载后资料丢失：%s", drama.ID)
		}
	}
	restored.mu.Lock()
	restoredChecked := len(restored.sourceRecords[sourceHuangguoVideo].MetadataChecked)
	restored.mu.Unlock()
	if restoredChecked != items {
		t.Fatalf("重载后 MetadataChecked = %d，期望 %d", restoredChecked, items)
	}
}

func TestSourceMetadataDeferredFlushFailureIsReported(t *testing.T) {
	stub := &sourceThroughputStub{items: 4}
	engine, _ := sourceThroughputEngine(t, stub)
	stub.breakWrites = func() {
		catalog := filepath.Join(engine.directory, "catalogs.json")
		_ = os.Remove(catalog)
		_ = os.Mkdir(catalog, 0o700)
	}
	engine.runSourceTask(context.Background(), sourceHuangguoVideo, "update", nativeDrama{}, &nativeSourceTask{cancel: func() {}})

	engine.mu.Lock()
	record := engine.sourceRecords[sourceHuangguoVideo]
	needsSave := record.NeedsSave
	engine.mu.Unlock()
	if !needsSave {
		t.Fatal("静默落盘失败未上报 NeedsSave")
	}
	status := engine.sourceStatus(sourceHuangguoVideo)
	if status.StorageError == "" {
		t.Fatal("静默落盘失败未给出可读错误")
	}
	if status.Stage != "等待保存" {
		t.Fatalf("静默落盘失败后 stage = %q，期望「等待保存」", status.Stage)
	}
}

func TestSourceDetailPersistencePaths(t *testing.T) {
	stub := &sourceThroughputStub{items: 1}
	drama := nativeDrama{ID: providerDramaID(sourceHuangguoVideo, "video/synthetic-0"), Source: sourceHuangguoVideo, SourceID: "video/synthetic-0", Title: "合成短剧0"}

	deferredEngine, _ := sourceThroughputEngine(t, stub)
	deferredEngine.mu.Lock()
	deferredEngine.catalogs[sourceHuangguoVideo] = []nativeDrama{drama}
	deferredEngine.mu.Unlock()
	deferredCtx := context.WithValue(context.Background(), nativeDeferredDetailKey{}, true)
	if _, err := deferredEngine.nativeDetail(deferredCtx, drama); err != nil {
		t.Fatal(err)
	}
	deferredReload := &nativeEngine{directory: deferredEngine.directory}
	deferredReload.loadCatalogCache()
	if len(deferredReload.catalogs[sourceHuangguoVideo]) != 0 {
		t.Fatal("资料阶段的详情请求不应立即落盘")
	}
	if err := deferredEngine.flushSourceProgress(sourceHuangguoVideo, true); err != nil {
		t.Fatal(err)
	}
	flushedReload := &nativeEngine{directory: deferredEngine.directory}
	flushedReload.loadCatalogCache()
	if items := flushedReload.catalogs[sourceHuangguoVideo]; len(items) != 1 || items[0].Episodes != 1 {
		t.Fatalf("强制落盘后详情仍丢失：%+v", items)
	}

	uiEngine, _ := sourceThroughputEngine(t, stub)
	uiEngine.mu.Lock()
	uiEngine.catalogs[sourceHuangguoVideo] = []nativeDrama{drama}
	uiEngine.mu.Unlock()
	if _, err := uiEngine.nativeDetail(context.Background(), drama); err != nil {
		t.Fatal(err)
	}
	uiReload := &nativeEngine{directory: uiEngine.directory}
	uiReload.loadCatalogCache()
	if items := uiReload.catalogs[sourceHuangguoVideo]; len(items) != 1 || items[0].Episodes != 1 {
		t.Fatalf("UI 详情路径未立即落盘：%+v", items)
	}
}

func hongguoExtraMetadataTransport(t *testing.T, requests *int) sourceFixtureTransport {
	t.Helper()
	var mu sync.Mutex
	return func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		if requests != nil {
			*requests++
		}
		mu.Unlock()
		id := request.URL.Query().Get("series_id")
		if id == "" && request.Body != nil {
			payload, _ := io.ReadAll(request.Body)
			var decoded map[string]any
			if json.Unmarshal(payload, &decoded) == nil {
				id = mapString(decoded, "series_id")
			}
		}
		if id == "" {
			id = "123"
		}
		if request.URL.Query().Get("series_id") != "" {
			body := fmt.Sprintf(`<script>window._ROUTER_DATA = {"loaderData":{"detail_page":{"seriesDetail":{"series_id":%q,"series_name":"合成红果剧","first_visible_time":"1700000000000"}}}}</script>`, id)
			return sourceFixtureResponse(request, http.StatusOK, body), nil
		}
		body := fmt.Sprintf(`{"data":{"video_data":{"series_id_str":%q,"episode_cnt":"1","video_list":[{"vid":"555","vid_index":"1","series_id":%q}]}}}`, id, id)
		return sourceFixtureResponse(request, http.StatusOK, body), nil
	}
}

func TestSourceExtraMetadataPersistencePaths(t *testing.T) {
	drama := nativeDrama{ID: providerDramaID(sourceHongguo, "123"), Source: sourceHongguo, Title: "合成红果剧"}

	deferredEngine := sourceFixtureEngine(t, hongguoExtraMetadataTransport(t, nil))
	deferredEngine.mu.Lock()
	deferredEngine.catalogs[sourceHongguo] = []nativeDrama{drama}
	deferredEngine.mu.Unlock()
	deferredCtx := context.WithValue(context.Background(), nativeDeferredDetailKey{}, true)
	fresh, err := deferredEngine.nativeExtraMetadata(deferredCtx, drama)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.OnlineDate == "" {
		t.Fatal("extra metadata 未取到资料")
	}
	deferredEngine.mu.Lock()
	writes := deferredEngine.catalogDiskWrites
	deferredEngine.mu.Unlock()
	if writes != 0 {
		t.Fatalf("静默 ctx 下 extra metadata 不应立即落盘：%d 次", writes)
	}
	deferredEngine.mu.Lock()
	deferredEngine.catalogs[sourceHongguo] = mergeNativeCatalog(deferredEngine.catalogs[sourceHongguo], []nativeDrama{fresh})
	deferredEngine.markSourceCatalogDirtyLocked(sourceHongguo)
	deferredEngine.mu.Unlock()
	if err := deferredEngine.flushSourceProgress(sourceHongguo, true); err != nil {
		t.Fatal(err)
	}
	flushedReload := &nativeEngine{directory: deferredEngine.directory}
	flushedReload.loadCatalogCache()
	if items := flushedReload.catalogs[sourceHongguo]; len(items) != 1 || items[0].OnlineDate == "" {
		t.Fatalf("强制落盘后 extra metadata 仍丢失：%+v", items)
	}

	uiEngine := sourceFixtureEngine(t, hongguoExtraMetadataTransport(t, nil))
	uiEngine.mu.Lock()
	uiEngine.catalogs[sourceHongguo] = []nativeDrama{drama}
	uiEngine.mu.Unlock()
	if _, err := uiEngine.nativeMetadata(context.Background(), drama); err != nil {
		t.Fatal(err)
	}
	uiReload := &nativeEngine{directory: uiEngine.directory}
	uiReload.loadCatalogCache()
	if items := uiReload.catalogs[sourceHongguo]; len(items) != 1 || items[0].OnlineDate == "" {
		t.Fatalf("UI metadata 路径未立即落盘：%+v", items)
	}
}

func runExtraMetadataPipeline(t *testing.T, items, flushItems int, deferDetail bool) (writes, requests int, elapsed time.Duration) {
	t.Helper()
	previousFlush, previousDefer := nativeMetadataFlushItems, nativeMetadataDeferDetailWrite
	nativeMetadataFlushItems, nativeMetadataDeferDetailWrite = flushItems, deferDetail
	defer func() { nativeMetadataFlushItems, nativeMetadataDeferDetailWrite = previousFlush, previousDefer }()

	engine := sourceFixtureEngine(t, hongguoExtraMetadataTransport(t, &requests))
	seeded := make([]nativeDrama, 0, items)
	for index := 0; index < items; index++ {
		seeded = append(seeded, nativeDrama{ID: providerDramaID(sourceHongguo, fmt.Sprintf("%d", 123+index)), Source: sourceHongguo, Title: fmt.Sprintf("合成红果剧%d", index)})
	}
	engine.mu.Lock()
	engine.catalogs[sourceHongguo] = seeded
	engine.mu.Unlock()

	started := time.Now()
	for {
		ready, remaining, _ := engine.sourceMetadataWork(sourceHongguo)
		if remaining == 0 || ready == 0 {
			break
		}
		if err := engine.updateSource(context.Background(), sourceHongguo, "continuousMetadata"); err != nil {
			t.Fatal(err)
		}
	}
	elapsed = time.Since(started)
	engine.mu.Lock()
	writes = engine.catalogDiskWrites
	engine.mu.Unlock()
	if ready, remaining, _ := engine.sourceMetadataWork(sourceHongguo); remaining != 0 || ready != 0 {
		t.Fatalf("资料补齐未完成：ready=%d remaining=%d", ready, remaining)
	}
	return writes, requests, elapsed
}

func TestSourceExtraMetadataContinuousPathWriteBatching(t *testing.T) {
	const items = 20
	oldWrites, oldRequests, oldElapsed := runExtraMetadataPipeline(t, items, 1, false)
	newWrites, newRequests, newElapsed := runExtraMetadataPipeline(t, items, 20, true)
	t.Logf("extraMetadata perItem  items=%d elapsed=%s requests=%d catalogsWrites=%d (%.2f/部)", items, oldElapsed.Round(time.Millisecond), oldRequests, oldWrites, float64(oldWrites)/float64(items))
	t.Logf("extraMetadata deferred items=%d elapsed=%s requests=%d catalogsWrites=%d (%.2f/部)", items, newElapsed.Round(time.Millisecond), newRequests, newWrites, float64(newWrites)/float64(items))
	if oldRequests != items*2 || newRequests != items*2 {
		t.Fatalf("每部应为 2 个请求（详情 + extra metadata）：old=%d new=%d", oldRequests, newRequests)
	}
	if newWrites >= oldWrites/2 {
		t.Fatalf("红果类来源落盘未批量：old=%d new=%d", oldWrites, newWrites)
	}
	if newElapsed >= oldElapsed {
		t.Fatalf("红果类来源批量后未变快：old=%s new=%s", oldElapsed, newElapsed)
	}
}
