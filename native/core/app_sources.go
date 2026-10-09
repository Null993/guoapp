package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const nativeSourceCatalogPageLimit = 1000

var nativeSourceRetryAttemptLimit = 8

type nativeSourceRetryExhaustedError struct {
	cause    error
	attempts int
}

func (failure *nativeSourceRetryExhaustedError) Error() string {
	return fmt.Sprintf("多次重试仍未成功（已重试 %d 次）：%s", failure.attempts, publicError(failure.cause).Error())
}

func (failure *nativeSourceRetryExhaustedError) Unwrap() error { return failure.cause }

func newSourceRetryExhaustedError(err error, attempts int) error {
	if err == nil {
		return nil
	}
	return &nativeSourceRetryExhaustedError{cause: err, attempts: attempts}
}

type nativeSourceTask struct {
	cancel context.CancelFunc
}

type nativeSourceRecord struct {
	Operation       string               `json:"operation"`
	Running         bool                 `json:"running"`
	Stage           string               `json:"stage"`
	Completed       int                  `json:"completed"`
	Total           int                  `json:"total"`
	Added           int                  `json:"added"`
	Error           string               `json:"error,omitempty"`
	NeedsSave       bool                 `json:"needsSave,omitempty"`
	StartedAt       time.Time            `json:"startedAt"`
	FinishedAt      time.Time            `json:"finishedAt"`
	RetryAt         time.Time            `json:"retryAt"`
	Health          *nativeSourceHealth  `json:"health,omitempty"`
	MetadataChecked map[string]time.Time `json:"metadataChecked,omitempty"`
	MetadataRetryAt map[string]time.Time `json:"metadataRetryAt,omitempty"`
	MetadataCursor  string               `json:"metadataCursor,omitempty"`
	CatalogStarted  bool                 `json:"catalogStarted,omitempty"`
}

type nativeSourceStatus struct {
	UnknownVIP   int                 `json:"unknownVip"`
	Source       string              `json:"source"`
	Count        int                 `json:"count"`
	Page         int                 `json:"page"`
	HasMore      bool                `json:"hasMore"`
	UpdatedAt    time.Time           `json:"updatedAt"`
	Operation    string              `json:"operation"`
	Running      bool                `json:"running"`
	Stage        string              `json:"stage"`
	Completed    int                 `json:"completed"`
	Total        int                 `json:"total"`
	Added        int                 `json:"added"`
	Error        string              `json:"error,omitempty"`
	StorageError string              `json:"storageError,omitempty"`
	StartedAt    time.Time           `json:"startedAt"`
	FinishedAt   time.Time           `json:"finishedAt"`
	RetryAt      time.Time           `json:"retryAt"`
	Health       *nativeSourceHealth `json:"health,omitempty"`
	Resumable    bool                `json:"resumable"`
}

func (engine *nativeEngine) lockSourceCatalog(ctx context.Context, source string) (func(), error) {
	engine.sourceCatalogMu.Lock()
	if engine.sourceCatalogs == nil {
		engine.sourceCatalogs = map[string]chan struct{}{}
	}
	gate := engine.sourceCatalogs[source]
	if gate == nil {
		gate = make(chan struct{}, 1)
		engine.sourceCatalogs[source] = gate
	}
	engine.sourceCatalogMu.Unlock()
	select {
	case gate <- struct{}{}:
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (engine *nativeEngine) loadSourceRecords() {
	engine.sourceRecords = map[string]nativeSourceRecord{}
	engine.sourceTasks = map[string]*nativeSourceTask{}
	path := filepath.Join(engine.directory, "sources.json")
	info, err := os.Stat(path)
	if err != nil || info.Size() > nativeSourceMaxBytes {
		return
	}
	body, err := os.ReadFile(path)
	var records map[string]nativeSourceRecord
	if err != nil || json.Unmarshal(body, &records) != nil {
		return
	}
	for source, record := range records {
		if !isHuangguoProviderSource(source) {
			continue
		}
		if record.Running {
			record.Running = false
			record.Stage = "任务已中断"
			record.Error = "上次任务未完成，已保留已更新内容，可重新开始"
		}
		if record.NeedsSave {
			record.NeedsSave = false
			record.Stage = "上次保存未完成"
			record.Error = "已恢复可读取的缓存，请重新更新"
			for key, state := range engine.catalogStates {
				if key == source || strings.HasPrefix(key, source+"|") {
					state.UpdatedAt = time.Time{}
					engine.catalogStates[key] = state
				}
			}
		}
		engine.sourceRecords[source] = record
	}
}

func (engine *nativeEngine) saveSourceRecordsLocked() error {
	body, err := json.Marshal(engine.sourceRecords)
	if err == nil && len(body) > nativeSourceMaxBytes {
		err = errNativeSourceLimit
	}
	if err == nil {
		err = writeNativeCacheFile(filepath.Join(engine.directory, "sources.json"), body)
	}
	engine.sourceSaveError = nativeSaveError("站源任务记录", err)
	return engine.sourceSaveError
}

func (engine *nativeEngine) sourceStatus(source string) nativeSourceStatus {
	source = canonicalProviderSource(source)
	engine.mu.Lock()
	defer engine.mu.Unlock()
	return engine.sourceStatusLocked(source)
}

func (engine *nativeEngine) sourceStatusLocked(source string) nativeSourceStatus {
	record := engine.sourceRecords[source]
	state, found := engine.catalogStates[source]
	warning := engine.storageWarningLocked()
	stage := record.Stage
	unknownVIP := 0
	if source == sourceHuangdou {
		for _, drama := range engine.catalogs[source] {
			if drama.VIP == nil {
				unknownVIP++
			}
		}
	}
	if warning != "" && !record.Running && stage == "已完成" {
		stage = "等待保存"
	}
	return nativeSourceStatus{Source: source, Count: len(engine.catalogs[source]), UnknownVIP: unknownVIP, Page: max(1, state.Page), HasMore: !found || state.HasMore,
		UpdatedAt: state.UpdatedAt, Operation: record.Operation, Running: record.Running, Stage: stage,
		Completed: record.Completed, Total: record.Total, Added: record.Added, Error: record.Error,
		StorageError: warning,
		StartedAt:    record.StartedAt, FinishedAt: record.FinishedAt, RetryAt: record.RetryAt, Health: record.Health,
		Resumable: record.CatalogStarted && !record.Running}
}

func (engine *nativeEngine) changeSourceRecord(source string, change func(*nativeSourceRecord)) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.sourceRecords == nil {
		engine.sourceRecords = map[string]nativeSourceRecord{}
	}
	record := engine.sourceRecords[source]
	change(&record)
	engine.sourceRecords[source] = record
	engine.saveSourceRecordsLocked()
}

func (engine *nativeEngine) startSourceTask(source, operation string, drama nativeDrama) (nativeSourceStatus, error) {
	source = canonicalProviderSource(source)
	if !nativeSourceAvailable(source) {
		return nativeSourceStatus{}, errNativeBuildSource
	}
	switch operation {
	case "update", "resume", "more", "metadata", "vipMetadata", "check", "checkCatalog", "retrySave":
	default:
		return nativeSourceStatus{}, errors.New("无效的站源操作")
	}
	if operation == "vipMetadata" && source != sourceHuangdou {
		return nativeSourceStatus{}, errors.New("当前站源不需要补齐 VIP 资料")
	}
	if drama.ID != "" && (!nativeDramaAvailable(drama) || sourceFromDramaID(drama.ID) != source) {
		return nativeSourceStatus{}, errNativeBuildSource
	}
	if operation == "retrySave" {
		return engine.retrySourceSave(source), nil
	}
	engine.mu.Lock()
	if engine.sourceTasks == nil {
		engine.sourceTasks = map[string]*nativeSourceTask{}
	}
	if task := engine.sourceTasks[source]; task != nil {
		status := engine.sourceStatusLocked(source)
		engine.mu.Unlock()
		return status, nil
	}
	if engine.sourceRecords == nil {
		engine.sourceRecords = map[string]nativeSourceRecord{}
	}
	previous := engine.sourceRecords[source]
	if time.Now().Before(previous.RetryAt) {
		engine.mu.Unlock()
		return nativeSourceStatus{}, fmt.Errorf("站源暂时暂停请求，请在 %d 秒后重试", max(1, int(time.Until(previous.RetryAt).Seconds()+.999)))
	}
	ctx, cancel := context.WithCancel(context.Background())
	task := &nativeSourceTask{cancel: cancel}
	engine.sourceTasks[source] = task
	previous.Operation, previous.Running, previous.Stage = operation, true, "准备中"
	if operation == "update" {
		previous.CatalogStarted = false
	}
	previous.Completed, previous.Total, previous.Added = 0, 0, 0
	previous.NeedsSave = false
	previous.Error, previous.StartedAt, previous.FinishedAt, previous.RetryAt = "", time.Now(), time.Time{}, time.Time{}
	engine.sourceRecords[source] = previous
	engine.saveSourceRecordsLocked()
	status := engine.sourceStatusLocked(source)
	engine.mu.Unlock()
	go engine.runSourceTask(ctx, source, operation, drama, task)
	return status, nil
}

func (engine *nativeEngine) cancelSourceTask(source string) nativeSourceStatus {
	source = canonicalProviderSource(source)
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if task := engine.sourceTasks[source]; task != nil {
		task.cancel()
		record := engine.sourceRecords[source]
		record.Stage = "正在停止"
		engine.sourceRecords[source] = record
	}
	return engine.sourceStatusLocked(source)
}

func (engine *nativeEngine) runSourceTask(ctx context.Context, source, operation string, drama nativeDrama, task *nativeSourceTask) {
	var err error
	defer func() {
		if recover() != nil {
			err = errors.New("站源任务处理失败，已保留缓存")
		}
		stopped := ctx.Err() != nil
		task.cancel()
		engine.mu.Lock()
		defer engine.mu.Unlock()
		delete(engine.sourceTasks, source)
		record := engine.sourceRecords[source]
		record.Running, record.FinishedAt = false, time.Now()
		record.Stage = "已完成"
		if err != nil {
			var exhausted *nativeSourceRetryExhaustedError
			switch {
			case stopped:
				record.Stage, record.Error = "已停止", "已保留更新内容，可稍后继续"
				record.RetryAt = time.Time{}
			case errors.As(err, &exhausted):
				record.Stage, record.Error = "多次重试仍未成功", publicError(exhausted).Error()
				record.RetryAt = time.Time{}
			default:
				record.Stage, record.Error = "未完成", publicError(err).Error()
				var persistence *nativePersistenceError
				if errors.As(err, &persistence) {
					record.NeedsSave, record.Error = engine.catalogSaveError != nil, ""
					record.Stage = "已保存，可继续更新"
					if record.NeedsSave {
						record.Stage = "等待保存"
					}
				}
				if errors.Is(err, context.Canceled) {
					record.Stage, record.Error = "已停止", "已保留更新内容，可稍后继续"
					record.RetryAt = time.Time{}
				}
				var backoff *requestBackoff
				if errors.As(err, &backoff) {
					record.RetryAt = backoff.until
					switch {
					case backoff.verify:
						record.Stage = "需要人工验证"
						record.Error = "站点要求浏览器验证或人机校验，请在站源管理的“验证与授权”里导入本人站点会话后重试"
						record.RetryAt = time.Time{}
					case backoff.status == http.StatusForbidden:
						record.Stage = "站点拒绝访问"
						record.Error = "站点拒绝了当前请求，请在站源管理的“验证与授权”里导入本人站点会话后重试"
					}
				}
				if errors.Is(err, context.DeadlineExceeded) {
					record.Stage = "请求超时"
					record.Error = "站源请求超时，已保留更新内容，可继续更新"
				}
				if isBoundedNetworkError(err) {
					record.Stage = "网络请求失败"
					record.Error = fmt.Sprintf("网络请求失败（%s），已保留更新内容，可稍后继续更新", publicError(err).Error())
					record.RetryAt = time.Time{}
				}
			}
		}
		engine.sourceRecords[source] = record
		engine.saveSourceRecordsLocked()
	}()
	if operation == "check" || operation == "checkCatalog" {
		err = engine.checkSource(ctx, source, drama, operation == "check")
	} else if operation == "update" || operation == "resume" {
		ctx = context.WithValue(ctx, backgroundCatalogKey{}, true)
		err = engine.runContinuousSourceUpdate(ctx, source, operation == "resume")
	} else {
		ctx = context.WithValue(ctx, backgroundCatalogKey{}, true)
		err = engine.updateSource(ctx, source, operation)
	}
}

func (engine *nativeEngine) runContinuousSourceUpdate(ctx context.Context, source string, resume bool) error {
	engine.mu.Lock()
	started := engine.sourceRecords[source].CatalogStarted
	engine.mu.Unlock()
	if !resume || !started {
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) { record.Stage = "查找新剧" })
		if err := engine.retrySourceStep(ctx, source, func() error {
			return engine.loadSourceCatalogPage(ctx, source, 1)
		}); err != nil {
			return err
		}
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) { record.CatalogStarted = true })
	}
	note := ""
	stalledPages := 0
	pageLimit := max(nativeSourceCatalogPageLimit, engine.downloader.cfg.MaxPagesPerSort)
	for {
		current := engine.nativeCached(source)
		if !current.HasMore {
			break
		}
		page := current.Page + 1
		if len(current.Items) == 0 {
			page = 1
		}
		if page > pageLimit {
			note = fmt.Sprintf("目录已加载 %d 页，达到本次更新上限", current.Page)
			break
		}
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
			record.Stage = fmt.Sprintf("加载目录第 %d 页", page)
			record.Completed, record.Total = current.Page, 0
		})
		if err := engine.retrySourceStep(ctx, source, func() error {
			return engine.loadSourceCatalogPage(ctx, source, page)
		}); err != nil {
			return err
		}
		next := engine.nativeCached(source)
		if next.HasMore && len(next.Items) <= len(current.Items) {
			stalledPages++
			if stalledPages >= 3 {
				note = "源站目录连续多页没有新增内容，已保留当前目录位置"
				break
			}
		} else {
			stalledPages = 0
		}
	}
	attempts := 0
	runTotal := 0
	runCompleted := 0
	for {
		ready, remaining, nextRetry := engine.sourceMetadataWork(source)
		if remaining == 0 {
			engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
				record.CatalogStarted = false
				if runTotal > 0 {
					record.Total, record.Completed = runTotal, runTotal
				}
				record.Stage = firstNonEmpty(note, "目录已完成，资料检查已结束")
			})
			return nil
		}
		if runTotal == 0 {
			runTotal = remaining
		}
		if ready == 0 {
			engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
				record.Stage = "等待资料重试，随后自动续跑"
				record.RetryAt = nextRetry
			})
			if err := waitSourceRetry(ctx, nextRetry); err != nil {
				return err
			}
			engine.changeSourceRecord(source, func(record *nativeSourceRecord) { record.RetryAt = time.Time{} })
			continue
		}
		runCompleted = sourceRunProgress(runTotal, remaining, runCompleted)
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
			record.Total, record.Completed = runTotal, runCompleted
		})
		err := engine.updateSource(ctx, source, "continuousMetadata")
		if err == nil {
			attempts = 0
			continue
		}
		attempts++
		retry, temporary := sourceRetryWait(err, attempts)
		if !temporary {
			if sourceRetryExhausted(err, attempts) {
				engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
					record.Stage = "多次重试仍未成功"
					record.RetryAt = time.Time{}
				})
				return newSourceRetryExhaustedError(err, attempts)
			}
			return err
		}
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
			record.Stage = "资料请求未成功，等待后自动续跑"
			record.RetryAt = retry
		})
		if waitErr := waitSourceRetry(ctx, retry); waitErr != nil {
			return waitErr
		}
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) { record.RetryAt = time.Time{} })
	}
}

func sourceRunProgress(runTotal, remaining, previous int) int {
	completed := runTotal - remaining
	if completed < previous {
		completed = previous
	}
	if completed < 0 {
		completed = 0
	}
	if completed > runTotal {
		completed = runTotal
	}
	return completed
}

func (engine *nativeEngine) sourceMetadataWork(source string) (ready, remaining int, nextRetry time.Time) {
	items := engine.nativeCached(source).Items
	engine.mu.Lock()
	record := engine.sourceRecords[source]
	engine.mu.Unlock()
	now := time.Now()
	for _, drama := range items {
		if drama.Episodes > 0 && drama.Description != "" && (source != sourceHuangdou || drama.VIP != nil) {
			continue
		}
		if now.Sub(record.MetadataChecked[drama.ID]) < 24*time.Hour {
			continue
		}
		remaining++
		until := record.MetadataRetryAt[drama.ID]
		if !now.Before(until) {
			ready++
		} else if nextRetry.IsZero() || until.Before(nextRetry) {
			nextRetry = until
		}
	}
	return ready, remaining, nextRetry
}

func (engine *nativeEngine) retrySourceStep(ctx context.Context, source string, step func() error) error {
	attempts := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := step()
		if err == nil {
			return nil
		}
		attempts++
		retry, temporary := sourceRetryWait(err, attempts)
		if !temporary {
			if sourceRetryExhausted(err, attempts) {
				engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
					record.Stage = "多次重试仍未成功"
					record.RetryAt = time.Time{}
				})
				return newSourceRetryExhaustedError(err, attempts)
			}
			return err
		}
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
			record.Stage = "请求未成功，等待后自动续跑"
			record.RetryAt = retry
		})
		if err := waitSourceRetry(ctx, retry); err != nil {
			return err
		}
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) { record.RetryAt = time.Time{} })
	}
}

func sourceRetryWait(err error, attempts int) (time.Time, bool) {
	retry, temporary := sourceRetryDelay(err)
	if !temporary {
		return time.Time{}, false
	}
	if attempts >= nativeSourceRetryAttemptLimit {
		return time.Time{}, false
	}
	var backoff *requestBackoff
	if errors.As(err, &backoff) {
		return retry, true
	}
	if attempts > 0 {
		retry = time.Now().Add(time.Duration(min(120, 3*attempts*attempts)) * time.Second)
	}
	return retry, true
}

func sourceRetryExhausted(err error, attempts int) bool {
	if attempts < nativeSourceRetryAttemptLimit {
		return false
	}
	_, temporary := sourceRetryDelay(err)
	return temporary
}

func isBoundedNetworkError(err error) bool {
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	var network net.Error
	return errors.As(err, &network) && !network.Timeout()
}

func sourceTerminalError(err error) bool {
	var verification *requestBackoff
	if errors.As(err, &verification) && verification.verify {
		return true
	}
	return isHuangdouAccessError(err) || isPermanentSourceError(err)
}

func sourceRetryDelay(err error) (time.Time, bool) {
	if errors.Is(err, context.Canceled) || sourceTerminalError(err) {
		return time.Time{}, false
	}
	var backoff *requestBackoff
	if errors.As(err, &backoff) {
		temporary := backoff.status == http.StatusTooManyRequests || backoff.status == http.StatusRequestTimeout || backoff.status >= 500
		return backoff.until, temporary
	}
	var transient *sourceTransientError
	if errors.As(err, &transient) {
		delay := 3 * time.Second
		if transient.status >= 500 {
			delay = 5 * time.Second
		}
		return time.Now().Add(delay), true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return time.Now().Add(3 * time.Second), true
	}
	var network net.Error
	if errors.As(err, &network) {
		return time.Now().Add(3 * time.Second), true
	}
	return time.Time{}, false
}

func waitSourceRetry(ctx context.Context, until time.Time) error {
	delay := time.Until(until)
	if delay < time.Second {
		delay = time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (engine *nativeEngine) updateSource(ctx context.Context, source, operation string) error {
	before := engine.nativeCached(source)
	count := len(before.Items)
	defer func() {
		after := len(engine.nativeCached(source).Items)
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) { record.Added = max(0, after-count) })
	}()
	if operation == "update" {
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) { record.Stage = "查找新剧" })
		if err := engine.loadSourceCatalogPage(ctx, source, 1); err != nil {
			return err
		}
	}
	if operation == "more" || operation == "update" {
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) { record.Stage = "继续加载历史分页" })
		limit := 1
		if operation == "update" {
			maxPages := engine.downloader.cfg.MaxPagesPerSort
			if maxPages <= 0 {
				maxPages = defaultConfig().MaxPagesPerSort
			}
			limit = max(0, maxPages-1)
		}
		if err := engine.loadMoreSourceCatalogPages(ctx, source, limit); err != nil {
			return err
		}
	}
	if operation == "more" {
		return nil
	}
	items := engine.nativeCached(source).Items
	var pending []nativeDrama
	engine.mu.Lock()
	record := engine.sourceRecords[source]
	start := 0
	for index, drama := range items {
		if drama.ID == record.MetadataCursor {
			start = index + 1
			break
		}
	}
	for scanned := 0; scanned < len(items); scanned++ {
		drama := items[(start+scanned)%len(items)]
		if len(pending) >= 8 {
			break
		}
		if operation == "vipMetadata" && drama.VIP != nil {
			continue
		}
		if operation != "vipMetadata" && time.Since(record.MetadataChecked[drama.ID]) < 24*time.Hour {
			continue
		}
		if time.Now().Before(record.MetadataRetryAt[drama.ID]) {
			continue
		}
		if operation == "metadata" || operation == "vipMetadata" || drama.Episodes <= 0 || drama.Description == "" || source == sourceHuangdou && drama.VIP == nil {
			pending = append(pending, drama)
		}
	}
	engine.mu.Unlock()
	engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
		record.Stage = "补齐剧集资料"
		if operation != "continuousMetadata" {
			record.Completed, record.Total = 0, len(pending)
		}
	})
	var failures []error
	for index, drama := range pending {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		result, err := engine.nativeDetail(ctx, drama)
		unknownVIP := false
		if err == nil {
			fresh := result.(map[string]any)["drama"].(nativeDrama)
			var metadataErr error
			if nativeNeedsExtraMetadata(fresh) {
				fresh, metadataErr = engine.nativeExtraMetadata(ctx, fresh)
			}
			unknownVIP = source == sourceHuangdou && fresh.VIP == nil
			engine.mu.Lock()
			engine.catalogs[source] = mergeNativeCatalog(engine.catalogs[source], []nativeDrama{fresh})
			for key, items := range engine.catalogs {
				if !strings.HasPrefix(key, source+"|") {
					continue
				}
				for index := range items {
					if items[index].ID == fresh.ID {
						items[index] = mergeNativeDrama(items[index], fresh)
					}
				}
			}
			err = engine.writeCatalogDiskLocked()
			engine.mu.Unlock()
			if err == nil {
				err = metadataErr
			}
		}
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
			if operation != "continuousMetadata" {
				record.Completed = index + 1
			}
			record.MetadataCursor = drama.ID
			if err == nil {
				if record.MetadataChecked == nil {
					record.MetadataChecked = map[string]time.Time{}
				}
				record.MetadataChecked[drama.ID] = time.Now()
				delete(record.MetadataRetryAt, drama.ID)
				if unknownVIP {
					if record.MetadataRetryAt == nil {
						record.MetadataRetryAt = map[string]time.Time{}
					}
					record.MetadataRetryAt[drama.ID] = time.Now().Add(5 * time.Minute)
				}
			} else if ctx.Err() == nil {
				if record.MetadataRetryAt == nil {
					record.MetadataRetryAt = map[string]time.Time{}
				}
				retry := 5 * time.Minute
				if strings.Contains(err.Error(), "404") {
					if record.MetadataChecked == nil {
						record.MetadataChecked = map[string]time.Time{}
					}
					record.MetadataChecked[drama.ID] = time.Now()
					retry = 24 * time.Hour
				}
				record.MetadataRetryAt[drama.ID] = time.Now().Add(retry)
			}
		})
		if err != nil {
			var persistence *nativePersistenceError
			if errors.As(err, &persistence) {
				return err
			}
			if !strings.Contains(err.Error(), "404") {
				failures = append(failures, err)
			}
			var backoff *requestBackoff
			if errors.As(err, &backoff) || len(failures) >= 2 {
				break
			}
		}
	}
	return errors.Join(failures...)
}

func (engine *nativeEngine) loadSourceCatalogPage(ctx context.Context, source string, page int) error {
	result, err := engine.nativeCatalog(ctx, nativeInput{Source: source, Page: page, Force: true})
	if err != nil {
		return err
	}
	if result.Warning != "" {
		if result.saveError != nil {
			return result.saveError
		}
		if result.catalogError != nil {
			return result.catalogError
		}
		return errors.New(result.Warning)
	}
	return nil
}

func (engine *nativeEngine) loadMoreSourceCatalogPages(ctx context.Context, source string, limit int) error {
	if limit <= 0 {
		return nil
	}
	for loaded := 0; loaded < limit; loaded++ {
		current := engine.nativeCached(source)
		if !current.HasMore {
			return nil
		}
		page := current.Page + 1
		if len(current.Items) == 0 {
			page = 1
		}
		if page > nativeSourceCatalogPageLimit {
			return errors.New("站源分页已达到接口范围，已保留目录位置")
		}
		if err := engine.loadSourceCatalogPage(ctx, source, page); err != nil {
			return err
		}
	}
	return nil
}
