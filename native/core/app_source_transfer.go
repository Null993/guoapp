package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	nativeSourceResultsVersion    = "sourceResults.v1"
	nativeSourceResultsMaxBytes   = 128 << 20
	nativeSourceResultsMaxItems   = 200000
	nativeSourceResultsMaxSources = 512

	nativeSourceResultsAppVersion = nativeAppVersion
)

type nativeSourceResultsFile struct {
	Version    string                     `json:"version"`
	ExportedAt time.Time                  `json:"exportedAt"`
	AppVersion string                     `json:"appVersion"`
	Platform   string                     `json:"platform,omitempty"`
	Sources    []nativeSourceResultsEntry `json:"sources"`
}

type nativeSourceResultsEntry struct {
	Source   string                    `json:"source"`
	Category string                    `json:"category,omitempty"`
	Record   nativeSourceResultsRecord `json:"record"`
	State    nativeSourceResultsState  `json:"state"`
	Items    []nativeDrama             `json:"items"`
}

type nativeSourceResultsRecord struct {
	Operation       string               `json:"operation,omitempty"`
	Running         bool                 `json:"running"`
	Stage           string               `json:"stage,omitempty"`
	Completed       int                  `json:"completed,omitempty"`
	Total           int                  `json:"total,omitempty"`
	Added           int                  `json:"added,omitempty"`
	NeedsSave       bool                 `json:"needsSave,omitempty"`
	CatalogStarted  bool                 `json:"catalogStarted,omitempty"`
	MetadataChecked map[string]time.Time `json:"metadataChecked,omitempty"`
	MetadataRetryAt map[string]time.Time `json:"metadataRetryAt,omitempty"`
	MetadataCursor  string               `json:"metadataCursor,omitempty"`
	StartedAt       time.Time            `json:"startedAt"`
	FinishedAt      time.Time            `json:"finishedAt"`
}

type nativeSourceResultsState struct {
	UpdatedAt time.Time `json:"updatedAt"`
	Page      int       `json:"page"`
	HasMore   bool      `json:"hasMore"`
}

type nativeSourceResultsImportPlan struct {
	entries        []nativeSourceResultsEntry
	records        map[string]nativeSourceResultsRecord
	skippedSources []string
	skippedItems   int
}

func nativeSourceResultsFileName(now time.Time) string {
	return fmt.Sprintf("zhenguojian-source-results-%s.json", now.Format("20060102150405"))
}

func nativeSourceResultsPath(path string, now time.Time) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("请先选择站源结果的保存位置")
	}
	if !filepath.IsAbs(path) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", errors.New("站源结果保存位置无效")
		}
		path = absolute
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return filepath.Join(path, nativeSourceResultsFileName(now)), nil
	}
	if strings.HasSuffix(path, "/") || strings.HasSuffix(path, string(os.PathSeparator)) {
		trimmed := strings.TrimRight(path, `/\`)
		if trimmed == "" {
			return "", errors.New("站源结果保存位置无效")
		}
		return filepath.Join(trimmed, nativeSourceResultsFileName(now)), nil
	}
	if filepath.Ext(path) == "" {
		return path + ".json", nil
	}
	return path, nil
}

func nativeSourceResultsImportPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("请先选择站源结果文件")
	}
	if !filepath.IsAbs(path) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", errors.New("站源结果文件位置无效")
		}
		path = absolute
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", errors.New("找不到站源结果文件，请重新选择")
	}
	if info.IsDir() {
		return "", errors.New("请选择站源结果文件而不是目录")
	}
	if info.Size() > nativeSourceResultsMaxBytes {
		return "", fmt.Errorf("站源结果文件超过 %d MiB 上限，已拒绝导入", nativeSourceResultsMaxBytes>>20)
	}
	return path, nil
}

func nativeSourceResultsRecordFrom(record nativeSourceRecord) nativeSourceResultsRecord {
	return nativeSourceResultsRecord{
		Operation: record.Operation, Running: false, Stage: record.Stage,
		Completed: record.Completed, Total: record.Total, Added: record.Added,
		NeedsSave: record.NeedsSave, CatalogStarted: record.CatalogStarted,
		MetadataChecked: cloneNativeSourceResultsTimeMap(record.MetadataChecked),
		MetadataRetryAt: cloneNativeSourceResultsTimeMap(record.MetadataRetryAt),
		MetadataCursor:  record.MetadataCursor,
		StartedAt:       record.StartedAt, FinishedAt: record.FinishedAt,
	}
}

func (engine *nativeEngine) exportSourceResults(path string) (map[string]any, error) {
	target, err := nativeSourceResultsPath(path, time.Now())
	if err != nil {
		return nil, err
	}
	body, sources, items, err := engine.sourceResultsSnapshot()
	if err != nil {
		return nil, err
	}
	if err := writeNativeSourceResultsFile(target, body); err != nil {
		return nil, err
	}
	return map[string]any{"sources": sources, "items": items, "bytes": len(body), "path": target}, nil
}

func (engine *nativeEngine) sourceResultsSnapshot() ([]byte, int, int, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	file := nativeSourceResultsFile{Version: nativeSourceResultsVersion, ExportedAt: time.Now(),
		AppVersion: nativeSourceResultsAppVersion, Platform: runtime.GOOS + "/" + runtime.GOARCH}
	keys := make([]string, 0, len(engine.catalogs))
	for key := range engine.catalogs {
		source, _, _ := strings.Cut(key, "|")
		if !isHuangguoProviderSource(source) || !nativeSourceAvailable(source) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	sources := map[string]bool{}
	items := 0
	for _, key := range keys {
		source, category, _ := strings.Cut(key, "|")
		source = canonicalProviderSource(source)
		state := engine.catalogStates[key]
		file.Sources = append(file.Sources, nativeSourceResultsEntry{
			Source: source, Category: category,
			Record: nativeSourceResultsRecordFrom(engine.sourceRecords[source]),
			State:  nativeSourceResultsState{UpdatedAt: state.UpdatedAt, Page: max(1, state.Page), HasMore: state.HasMore},
			Items:  append([]nativeDrama{}, engine.catalogs[key]...),
		})
		sources[source] = true
		items += len(engine.catalogs[key])
	}
	if items > nativeSourceResultsMaxItems {
		return nil, 0, 0, fmt.Errorf("站源目录超过 %d 条上限，请先减少站源内容后再导出", nativeSourceResultsMaxItems)
	}
	body, err := json.Marshal(file)
	if err != nil {
		return nil, 0, 0, errors.New("无法生成站源结果文件")
	}
	if len(body) > nativeSourceResultsMaxBytes {
		return nil, 0, 0, fmt.Errorf("站源结果超过 %d MiB 上限，请先减少站源目录后再导出", nativeSourceResultsMaxBytes>>20)
	}
	return body, len(sources), items, nil
}

func writeNativeSourceResultsFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("无法创建站源结果导出目录，请检查保存位置")
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".source-results-*")
	if err != nil {
		return errors.New("无法写入站源结果文件，请检查目录写入权限")
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(body); err != nil {
		temporary.Close()
		return errors.New("无法写入站源结果文件，请检查剩余空间")
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return errors.New("无法写入站源结果文件，请检查剩余空间")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("无法写入站源结果文件，请检查剩余空间")
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return errors.New("无法保存站源结果文件，请检查目录写入权限")
	}
	return nil
}

func (engine *nativeEngine) importSourceResults(path string) (map[string]any, error) {
	target, err := nativeSourceResultsImportPath(path)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(target)
	if err != nil {
		return nil, errors.New("无法读取站源结果文件，请重新选择")
	}
	if len(body) > nativeSourceResultsMaxBytes {
		return nil, fmt.Errorf("站源结果文件超过 %d MiB 上限，已拒绝导入", nativeSourceResultsMaxBytes>>20)
	}
	var file nativeSourceResultsFile
	if json.Unmarshal(body, &file) != nil {
		return nil, errors.New("站源结果文件无法解析，请确认选择的是本应用导出的 JSON")
	}
	plan, err := planNativeSourceResults(file)
	if err != nil {
		return nil, err
	}
	if len(plan.entries) == 0 {
		return nil, fmt.Errorf("站源结果中的站源在当前版本不可用：%s", strings.Join(plan.skippedSources, "、"))
	}
	return engine.applySourceResults(plan, target, len(body))
}

func planNativeSourceResults(file nativeSourceResultsFile) (nativeSourceResultsImportPlan, error) {
	plan := nativeSourceResultsImportPlan{records: map[string]nativeSourceResultsRecord{}, skippedSources: []string{}}
	if file.Version != nativeSourceResultsVersion {
		return plan, fmt.Errorf("站源结果格式版本不受支持：%s（当前支持 %s）", firstNonEmpty(strings.TrimSpace(file.Version), "未标注"), nativeSourceResultsVersion)
	}
	if len(file.Sources) == 0 {
		return plan, errors.New("站源结果文件不包含站源内容")
	}
	if len(file.Sources) > nativeSourceResultsMaxSources {
		return plan, fmt.Errorf("站源结果包含 %d 个站源，超过 %d 上限", len(file.Sources), nativeSourceResultsMaxSources)
	}
	seen := map[string]bool{}
	total := 0
	for _, entry := range file.Sources {
		source := canonicalProviderSource(entry.Source)
		if !isHuangguoProviderSource(source) {
			return plan, fmt.Errorf("站源结果包含无法识别的站源：%s", firstNonEmpty(strings.TrimSpace(entry.Source), "空"))
		}
		category := strings.TrimSpace(entry.Category)
		if category != "" && !validNativeCategory(source, category) {
			return plan, fmt.Errorf("站源 %s 的分类 %s 无效", source, category)
		}
		key := nativeCatalogKey(source, category)
		if seen[key] {
			return plan, fmt.Errorf("站源结果中 %s 重复，已拒绝导入", key)
		}
		seen[key] = true
		total += len(entry.Items)
		if total > nativeSourceResultsMaxItems {
			return plan, fmt.Errorf("站源结果条目超过 %d 上限，已拒绝导入", nativeSourceResultsMaxItems)
		}
		if !nativeSourceAvailable(source) {
			if !nativeSourceResultsHas(plan.skippedSources, source) {
				plan.skippedSources = append(plan.skippedSources, source)
			}
			continue
		}
		accepted := make([]nativeDrama, 0, len(entry.Items))
		for _, item := range entry.Items {
			drama, valid := nativeSourceResultsDrama(item, source)
			if !valid {
				plan.skippedItems++
				continue
			}
			accepted = append(accepted, drama)
		}
		entry.Source, entry.Category, entry.Items = source, category, accepted
		plan.entries = append(plan.entries, entry)
	}
	for _, entry := range plan.entries {
		if entry.Category == "" {
			plan.records[entry.Source] = entry.Record
		}
	}
	for _, entry := range plan.entries {
		if _, found := plan.records[entry.Source]; !found {
			plan.records[entry.Source] = entry.Record
		}
	}
	return plan, nil
}

func nativeSourceResultsDrama(item nativeDrama, source string) (nativeDrama, bool) {
	itemSource, sourceID, ok := splitProviderDramaID(item.ID)
	if !ok || itemSource != source {
		return nativeDrama{}, false
	}
	item = migrateNativeDrama(item)
	item.Source, item.SourceID = source, sourceID
	return item, true
}

func nativeSourceResultsHas(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (engine *nativeEngine) applySourceResults(plan nativeSourceResultsImportPlan, path string, bytes int) (map[string]any, error) {
	engine.mu.Lock()
	for _, entry := range plan.entries {
		if engine.sourceTasks[entry.Source] != nil {
			engine.mu.Unlock()
			return nil, fmt.Errorf("站源 %s 正在更新，请先停止后再导入", entry.Source)
		}
	}
	previousCatalogs, previousStates, previousRecords := engine.catalogs, engine.catalogStates, engine.sourceRecords
	catalogs := make(map[string][]nativeDrama, len(previousCatalogs)+len(plan.entries))
	for key, items := range previousCatalogs {
		catalogs[key] = items
	}
	states := make(map[string]nativeCatalogState, len(previousStates)+len(plan.entries))
	for key, state := range previousStates {
		states[key] = state
	}
	records := make(map[string]nativeSourceRecord, len(previousRecords)+len(plan.records))
	for key, record := range previousRecords {
		records[key] = record
	}
	counts := map[string]int{}
	for _, entry := range plan.entries {
		key := nativeCatalogKey(entry.Source, entry.Category)
		catalogs[key] = mergeNativeCatalog(catalogs[key], entry.Items)
		if entry.Category != "" {
			catalogs[entry.Source] = mergeNativeCatalog(catalogs[entry.Source], entry.Items)
		}
		state := states[key]
		if state.Page < 1 || entry.State.Page >= state.Page {
			state.Page, state.HasMore = max(1, entry.State.Page), entry.State.HasMore
		}
		if entry.State.UpdatedAt.After(state.UpdatedAt) {
			state.UpdatedAt = entry.State.UpdatedAt
		}
		state.Warning = ""
		states[key] = state
		counts[entry.Source] += len(entry.Items)
	}
	for source, imported := range plan.records {
		record := records[source]
		record.Operation, record.Stage = imported.Operation, imported.Stage
		record.Running, record.Error, record.RetryAt, record.NeedsSave = false, "", time.Time{}, false
		record.Completed, record.Total, record.Added = imported.Completed, imported.Total, imported.Added
		record.CatalogStarted, record.MetadataCursor = imported.CatalogStarted, imported.MetadataCursor
		record.MetadataChecked = cloneNativeSourceResultsTimeMap(imported.MetadataChecked)
		record.MetadataRetryAt = cloneNativeSourceResultsTimeMap(imported.MetadataRetryAt)
		record.StartedAt, record.FinishedAt = imported.StartedAt, imported.FinishedAt
		records[source] = record
	}
	engine.catalogs, engine.catalogStates, engine.sourceRecords = catalogs, states, records
	if err := engine.writeCatalogDiskLocked(); err != nil {
		engine.restoreSourceResultsLocked(previousCatalogs, previousStates, previousRecords)
		engine.mu.Unlock()
		return nil, err
	}
	if err := engine.saveSourceRecordsLocked(); err != nil {
		engine.restoreSourceResultsLocked(previousCatalogs, previousStates, previousRecords)
		engine.mu.Unlock()
		return nil, err
	}
	engine.mu.Unlock()
	sources := make([]string, 0, len(counts))
	items := 0
	for source, count := range counts {
		sources = append(sources, source)
		items += count
	}
	sort.Strings(sources)
	details := make([]map[string]any, 0, len(sources))
	for _, source := range sources {
		details = append(details, map[string]any{"source": source, "items": counts[source]})
	}
	return map[string]any{"sources": len(sources), "items": items, "bytes": bytes, "path": path,
		"details": details, "skippedSources": plan.skippedSources, "skippedItems": plan.skippedItems}, nil
}

func (engine *nativeEngine) restoreSourceResultsLocked(catalogs map[string][]nativeDrama, states map[string]nativeCatalogState, records map[string]nativeSourceRecord) {
	engine.catalogs, engine.catalogStates, engine.sourceRecords = catalogs, states, records
	engine.writeCatalogDiskLocked()
	engine.saveSourceRecordsLocked()
}

func cloneNativeSourceResultsTimeMap(values map[string]time.Time) map[string]time.Time {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]time.Time, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
