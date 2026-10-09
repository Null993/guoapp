package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sourceResultsTestEngine(t *testing.T) *nativeEngine {
	t.Helper()
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.downloads.close)
	return engine
}

func sourceResultsTestFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func sourceResultsStatInt(t *testing.T, stats map[string]any, key string) int {
	t.Helper()
	value, ok := stats[key].(int)
	if !ok {
		t.Fatalf("统计字段 %s 类型错误：%#v", key, stats[key])
	}
	return value
}

func TestSourceResultsExportImportRestoresCatalogProgress(t *testing.T) {
	engine := sourceResultsTestEngine(t)
	source := sourceHongguo
	checkedAt := time.Date(2025, 9, 21, 10, 30, 0, 0, time.UTC)
	engine.mu.Lock()
	engine.catalogs[source] = []nativeDrama{
		{MetadataSchema: 1, ID: "hongguo:1", Source: source, SourceID: "1", Title: "合成短剧一", Description: "完整资料", Episodes: 12, Cover: "https://cdn.example.test/a.jpg"},
		{MetadataSchema: 1, ID: "hongguo:2", Source: source, SourceID: "2", Title: "合成短剧二"},
	}
	engine.catalogStates[source] = nativeCatalogState{Page: 7, HasMore: true, UpdatedAt: checkedAt}
	engine.sourceRecords[source] = nativeSourceRecord{Operation: "resume", Running: true, Stage: "加载目录第 7 页",
		Completed: 7, Added: 4, CatalogStarted: true, MetadataCursor: "hongguo:2",
		MetadataChecked: map[string]time.Time{"hongguo:1": checkedAt},
		MetadataRetryAt: map[string]time.Time{"hongguo:2": checkedAt.Add(time.Hour)}}
	engine.mu.Unlock()

	path := filepath.Join(t.TempDir(), "results.json")
	stats, err := engine.exportSourceResults(path)
	if err != nil {
		t.Fatal(err)
	}
	if sourceResultsStatInt(t, stats, "sources") != 1 || sourceResultsStatInt(t, stats, "items") != 2 {
		t.Fatalf("导出统计不符：%#v", stats)
	}
	if stats["path"] != path || sourceResultsStatInt(t, stats, "bytes") <= 0 {
		t.Fatalf("导出路径或大小不符：%#v", stats)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var exported nativeSourceResultsFile
	if err := json.Unmarshal(body, &exported); err != nil {
		t.Fatal(err)
	}
	if exported.Version != nativeSourceResultsVersion || len(exported.Sources) != 1 {
		t.Fatalf("导出格式不符：%+v", exported)
	}
	if exported.Sources[0].Record.Running {
		t.Fatal("导出记录不得标记为运行中")
	}
	if exported.Sources[0].State.Page != 7 || !exported.Sources[0].State.HasMore || exported.Sources[0].State.UpdatedAt.IsZero() {
		t.Fatalf("导出缺少分页位置：%+v", exported.Sources[0].State)
	}

	engine.mu.Lock()
	engine.catalogs = map[string][]nativeDrama{}
	engine.catalogStates = map[string]nativeCatalogState{}
	engine.sourceRecords = map[string]nativeSourceRecord{}
	engine.mu.Unlock()

	imported, err := engine.importSourceResults(path)
	if err != nil {
		t.Fatal(err)
	}
	if sourceResultsStatInt(t, imported, "sources") != 1 || sourceResultsStatInt(t, imported, "items") != 2 {
		t.Fatalf("导入统计不符：%#v", imported)
	}
	page := engine.nativeCached(source)
	if len(page.Items) != 2 || page.Items[0].Title != "合成短剧一" || page.Page != 7 || !page.HasMore {
		t.Fatalf("导入后目录与分页位置不符：%+v", page)
	}
	if page.Page+1 != 8 {
		t.Fatalf("接着更新应从第 8 页开始，实际为 %d", page.Page+1)
	}
	status := engine.sourceStatus(source)
	if status.Count != 2 || status.Page != 7 || !status.HasMore || status.Running || !status.Resumable {
		t.Fatalf("导入后状态不符：%+v", status)
	}
	engine.mu.Lock()
	record := engine.sourceRecords[source]
	state := engine.catalogStates[source]
	engine.mu.Unlock()
	if !record.CatalogStarted || record.Operation != "resume" || record.MetadataCursor != "hongguo:2" || record.MetadataChecked["hongguo:1"].IsZero() ||
		record.MetadataRetryAt["hongguo:2"].IsZero() || !state.UpdatedAt.Equal(checkedAt) {
		t.Fatalf("导入后更新进度不符：%+v / %+v", record, state)
	}
}

func TestSourceResultsImportOnOtherDeviceResumesAtExportedPage(t *testing.T) {
	exporter := sourceResultsTestEngine(t)
	source := sourceHongguo
	exporter.mu.Lock()
	exporter.catalogs[source] = []nativeDrama{{MetadataSchema: 1, ID: "hongguo:9", Source: source, SourceID: "9", Title: "跨设备短剧", Episodes: 30}}
	exporter.catalogStates[source] = nativeCatalogState{Page: 12, HasMore: true, UpdatedAt: time.Now()}
	exporter.sourceRecords[source] = nativeSourceRecord{Operation: "update", Stage: "已停止", CatalogStarted: true, Added: 9}
	exporter.mu.Unlock()
	path := filepath.Join(t.TempDir(), "results.json")
	if _, err := exporter.exportSourceResults(path); err != nil {
		t.Fatal(err)
	}

	other := sourceResultsTestEngine(t)
	stats, err := other.importSourceResults(path)
	if err != nil {
		t.Fatal(err)
	}
	if sourceResultsStatInt(t, stats, "items") != 1 {
		t.Fatalf("导入统计不符：%#v", stats)
	}
	status := other.sourceStatus(source)
	if status.Count != 1 || status.Page != 12 || !status.HasMore || !status.Resumable || status.Running {
		t.Fatalf("另一设备导入后应可从第 12 页继续：%+v", status)
	}
	if other.nativeCached(source).Page+1 != 13 {
		t.Fatalf("接着更新应从第 13 页开始，实际为 %d", other.nativeCached(source).Page+1)
	}
}

func TestSourceResultsExportOmitsSourceCredentials(t *testing.T) {
	engine := sourceResultsTestEngine(t)
	secrets := []string{"SECRET_SESSION_COOKIE_9f3a1c", "SECRET_UA/1.0", "SECRET_TOKEN_42", "SECRET_DEVICE_7", "secret-origin.test"}
	engine.downloader.sourceAccessMu.Lock()
	engine.downloader.sourceAccess = map[string]nativeSourceAccess{sourceHongguo: {
		Source: sourceHongguo, Origin: "https://secret-origin.test",
		Cookie: secrets[0], UserAgent: secrets[1], Token: secrets[2], DeviceID: secrets[3],
	}}
	engine.downloader.sourceAccessMu.Unlock()
	engine.mu.Lock()
	engine.catalogs[sourceHongguo] = []nativeDrama{{MetadataSchema: 1, ID: "hongguo:1", Source: sourceHongguo, SourceID: "1", Title: "合成短剧", Cover: "https://cdn.example.test/a.jpg"}}
	engine.sourceRecords[sourceHongguo] = nativeSourceRecord{Operation: "update", Added: 1}
	engine.mu.Unlock()

	path := filepath.Join(t.TempDir(), "results.json")
	if _, err := engine.exportSourceResults(path); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(body))
	for _, secret := range append(append([]string{}, secrets...), "sourceaccess", "cookie", "useragent", "deviceid", "accesstoken") {
		if strings.Contains(text, strings.ToLower(secret)) {
			t.Fatalf("导出文件包含站点凭据内容：%s", secret)
		}
	}
}

func TestSourceResultsImportRejectsInvalidFiles(t *testing.T) {
	engine := sourceResultsTestEngine(t)
	engine.mu.Lock()
	engine.catalogs[sourceHongguo] = []nativeDrama{{MetadataSchema: 1, ID: "hongguo:keep", Source: sourceHongguo, SourceID: "keep", Title: "保留短剧"}}
	engine.sourceRecords[sourceHongguo] = nativeSourceRecord{Operation: "update", Added: 5}
	engine.mu.Unlock()
	oversized := strings.Repeat("{},", nativeSourceResultsMaxItems)
	cases := []struct {
		name string
		body string
	}{
		{"bad-version.json", `{"version":"otherResults.v9","sources":[{"source":"hongguo","items":[]}]}`},
		{"unknown-source.json", `{"version":"sourceResults.v1","sources":[{"source":"not-a-source","items":[]}]}`},
		{"partial.json", `{"version":"sourceResults.v1","sources":[{"source":"hongguo","items":[{"id":"hongguo:x"}]},{"source":"not-a-source","items":[]}]}`},
		{"empty.json", `{"version":"sourceResults.v1","sources":[]}`},
		{"malformed.json", `{"version":"sourceResults.v1",`},
		{"too-many.json", fmt.Sprintf(`{"version":"sourceResults.v1","sources":[{"source":"hongguo","items":[%s{}]}]}`, oversized)},
	}
	for _, testCase := range cases {
		path := sourceResultsTestFile(t, testCase.name, testCase.body)
		if _, err := engine.importSourceResults(path); err == nil {
			t.Fatalf("%s 应被拒绝", testCase.name)
		}
		page := engine.nativeCached(sourceHongguo)
		engine.mu.Lock()
		record := engine.sourceRecords[sourceHongguo]
		engine.mu.Unlock()
		if len(page.Items) != 1 || page.Items[0].ID != "hongguo:keep" || record.Added != 5 {
			t.Fatalf("%s 被拒绝后本机状态发生了变化：%+v / %+v", testCase.name, page, record)
		}
	}
}

func TestSourceResultsDispatchRoundTrip(t *testing.T) {
	response := NativeRequest(fmt.Sprintf(`{"action":"initialize","directory":%q}`, t.TempDir()))
	var init map[string]any
	if err := json.Unmarshal([]byte(response), &init); err != nil || init["ok"] != true {
		t.Fatalf("本地核心初始化失败：%s", response)
	}
	nativeState.Lock()
	engine := nativeState.engine
	nativeState.Unlock()
	if engine == nil {
		t.Fatal("本地核心未就绪")
	}
	t.Cleanup(func() {
		engine.downloads.close()
		nativeState.Lock()
		if nativeState.engine == engine {
			nativeState.engine = nil
		}
		nativeState.Unlock()
	})
	engine.mu.Lock()
	engine.catalogs[sourceHongguo] = []nativeDrama{{MetadataSchema: 1, ID: "hongguo:7", Source: sourceHongguo, SourceID: "7", Title: "边界短剧"}}
	engine.catalogStates[sourceHongguo] = nativeCatalogState{Page: 3, HasMore: true, UpdatedAt: time.Now()}
	engine.sourceRecords[sourceHongguo] = nativeSourceRecord{Operation: "update", CatalogStarted: true, Added: 2}
	engine.mu.Unlock()
	path := filepath.Join(t.TempDir(), "dispatch.json")

	response = NativeRequest(fmt.Sprintf(`{"action":"exportSourceResults","path":%q}`, path))
	var exported map[string]any
	if err := json.Unmarshal([]byte(response), &exported); err != nil || exported["ok"] != true {
		t.Fatalf("导出站源结果失败：%s", response)
	}
	exportedData, _ := exported["data"].(map[string]any)
	if exportedData["path"] != path || int(exportedData["sources"].(float64)) != 1 || int(exportedData["items"].(float64)) != 1 {
		t.Fatalf("导出返回不符：%s", response)
	}

	engine.mu.Lock()
	engine.catalogs = map[string][]nativeDrama{}
	engine.catalogStates = map[string]nativeCatalogState{}
	engine.sourceRecords = map[string]nativeSourceRecord{}
	engine.mu.Unlock()
	response = NativeRequest(fmt.Sprintf(`{"action":"importSourceResults","path":%q}`, path))
	var imported map[string]any
	if err := json.Unmarshal([]byte(response), &imported); err != nil || imported["ok"] != true {
		t.Fatalf("导入站源结果失败：%s", response)
	}
	importedData, _ := imported["data"].(map[string]any)
	details, _ := importedData["details"].([]any)
	if int(importedData["sources"].(float64)) != 1 || int(importedData["items"].(float64)) != 1 || len(details) != 1 {
		t.Fatalf("导入返回不符：%s", response)
	}
	status := engine.sourceStatus(sourceHongguo)
	if status.Count != 1 || status.Page != 3 || !status.Resumable || status.Running {
		t.Fatalf("导入后状态不符：%+v", status)
	}

	response = NativeRequest(`{"action":"importSourceResults","path":""}`)
	var rejected map[string]any
	if err := json.Unmarshal([]byte(response), &rejected); err != nil || rejected["ok"] != false || rejected["error"] == "" {
		t.Fatalf("空路径导入应返回可读错误：%s", response)
	}
}

func TestSourceResultsImportMergesWithoutReplacingOtherSources(t *testing.T) {
	previousBuild := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = previousBuild }()

	exporter := sourceResultsTestEngine(t)
	exporter.mu.Lock()
	exporter.catalogs[sourceHongguo] = []nativeDrama{{MetadataSchema: 1, ID: "hongguo:1", Source: sourceHongguo, SourceID: "1", Title: "红果短剧"}}
	exporter.catalogStates[sourceHongguo] = nativeCatalogState{Page: 4, HasMore: true, UpdatedAt: time.Now()}
	exporter.sourceRecords[sourceHongguo] = nativeSourceRecord{Operation: "resume", CatalogStarted: true}
	exporter.mu.Unlock()
	path := filepath.Join(t.TempDir(), "results.json")
	if _, err := exporter.exportSourceResults(path); err != nil {
		t.Fatal(err)
	}

	target := sourceResultsTestEngine(t)
	target.mu.Lock()
	target.catalogs[sourceHuangguoAI] = []nativeDrama{{MetadataSchema: 1, ID: "huangguoai:1", Source: sourceHuangguoAI, SourceID: "1", Title: "本机短剧"}}
	target.catalogStates[sourceHuangguoAI] = nativeCatalogState{Page: 2, HasMore: true, UpdatedAt: time.Now()}
	target.sourceRecords[sourceHuangguoAI] = nativeSourceRecord{Operation: "update", Added: 3}
	target.catalogs[sourceHongguo] = []nativeDrama{{MetadataSchema: 1, ID: "hongguo:2", Source: sourceHongguo, SourceID: "2", Title: "本机红果短剧"}}
	target.catalogStates[sourceHongguo] = nativeCatalogState{Page: 9, HasMore: true, UpdatedAt: time.Now()}
	target.mu.Unlock()

	stats, err := target.importSourceResults(path)
	if err != nil {
		t.Fatal(err)
	}
	if sourceResultsStatInt(t, stats, "sources") != 1 || sourceResultsStatInt(t, stats, "items") != 1 {
		t.Fatalf("导入统计不符：%#v", stats)
	}
	hongguo := target.nativeCached(sourceHongguo)
	if len(hongguo.Items) != 2 || hongguo.Page != 9 || !hongguo.HasMore {
		t.Fatalf("导入应合并目录并保留本机分页进度：%+v", hongguo)
	}
	if !target.sourceStatus(sourceHongguo).Resumable {
		t.Fatal("导入后红果应可接着更新")
	}
	other := target.nativeCached(sourceHuangguoAI)
	if len(other.Items) != 1 || other.Page != 2 {
		t.Fatalf("导入不得影响其它站源：%+v", other)
	}
	engine := target
	engine.mu.Lock()
	record := engine.sourceRecords[sourceHuangguoAI]
	engine.mu.Unlock()
	if record.Added != 3 || record.CatalogStarted {
		t.Fatalf("导入不得改写其它站源记录：%+v", record)
	}
}
