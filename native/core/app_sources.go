package core

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const nativeSourceCatalogPageLimit = 1000

const nativeMetadataFlushInterval = 2 * time.Second

var (
	nativeMetadataWorkers          = 6
	nativeMetadataFlushItems       = 20
	nativeMetadataDeferDetailWrite = true
)

var nativeSourceRetryAttemptLimit = 8

type nativeDeferredDetailKey struct{}

type nativeMetadataFetchKey struct{}

type sourceRetryWindowKey struct{}

type nativeSourceRetryPolicy struct {
	limit time.Duration
	now   func() time.Time
	wait  func(context.Context, time.Time) error
}

type nativeSourceRetryWindow struct {
	policy  nativeSourceRetryPolicy
	started time.Time
	cause   error
}

type nativeSourceRetryWindowError struct{ cause error }

func (err *nativeSourceRetryWindowError) Error() string {
	return "站源连续失败已达到 60 分钟，已保留更新内容，可接着更新：" + publicError(err.cause).Error()
}

func (err *nativeSourceRetryWindowError) Unwrap() error { return err.cause }

func (engine *nativeEngine) sourceNow() time.Time {
	if custom := engine.sourceRetryPolicy; custom != nil && custom.now != nil {
		return custom.now()
	}
	return time.Now()
}

func (engine *nativeEngine) newSourceRetryWindow() *nativeSourceRetryWindow {
	policy := nativeSourceRetryPolicy{limit: time.Hour, now: time.Now}
	if custom := engine.sourceRetryPolicy; custom != nil {
		if custom.limit > 0 {
			policy.limit = custom.limit
		}
		if custom.now != nil {
			policy.now = custom.now
		}
		if custom.wait != nil {
			policy.wait = custom.wait
		}
	}
	if policy.wait == nil {
		now := policy.now
		policy.wait = func(ctx context.Context, until time.Time) error { return waitSourceRetryAt(ctx, until, now) }
	}
	return &nativeSourceRetryWindow{policy: policy}
}

func (window *nativeSourceRetryWindow) failed(err error) {
	if window.started.IsZero() {
		window.started = window.policy.now()
	}
	window.cause = err
}

func (window *nativeSourceRetryWindow) reset() { window.started, window.cause = time.Time{}, nil }

func (window *nativeSourceRetryWindow) requestContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if window.started.IsZero() {
		work, cancel := context.WithCancel(ctx)
		return work, cancel, nil
	}
	remaining := window.started.Add(window.policy.limit).Sub(window.policy.now())
	if remaining <= 0 {
		return nil, nil, &nativeSourceRetryWindowError{cause: window.cause}
	}
	work, cancel := context.WithTimeout(ctx, remaining)
	return work, cancel, nil
}

func (window *nativeSourceRetryWindow) wait(ctx context.Context, until time.Time) error {
	if window.started.IsZero() {
		return window.policy.wait(ctx, until)
	}
	remaining := window.started.Add(window.policy.limit).Sub(window.policy.now())
	if remaining <= 0 {
		return &nativeSourceRetryWindowError{cause: window.cause}
	}
	bounded, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	deadline := window.policy.now().Add(remaining)
	if until.After(deadline) {
		until = deadline
	}
	err := window.policy.wait(bounded, until)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) || !window.policy.now().Before(deadline) {
		return &nativeSourceRetryWindowError{cause: window.cause}
	}
	return err
}

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

	pendingProgressFlush int
	catalogDirty         bool
	recordDirty          bool
	progressFlushedAt    time.Time
	catalogFlushCount    int
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
		Resumable: (record.CatalogStarted || record.Stage == "连续失败已暂停") && !record.Running}
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

func (engine *nativeEngine) changeSourceRecordDeferred(source string, change func(*nativeSourceRecord)) error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.sourceRecords == nil {
		engine.sourceRecords = map[string]nativeSourceRecord{}
	}
	record := engine.sourceRecords[source]
	change(&record)
	engine.sourceRecords[source] = record
	engine.markSourceRecordDirtyLocked(source)
	return engine.flushSourceProgressLocked(source, false)
}

func (engine *nativeEngine) markSourceCatalogDirtyLocked(source string) {
	record := engine.sourceRecords[source]
	record.catalogDirty = true
	engine.sourceRecords[source] = record
}

func (engine *nativeEngine) markSourceRecordDirtyLocked(source string) {
	record := engine.sourceRecords[source]
	record.recordDirty = true
	record.pendingProgressFlush++
	engine.sourceRecords[source] = record
}

func (engine *nativeEngine) flushSourceProgress(source string, force bool) error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	return engine.flushSourceProgressLocked(source, force)
}

func (engine *nativeEngine) flushSourceProgressLocked(source string, force bool) error {
	record := engine.sourceRecords[source]
	if !record.catalogDirty && !record.recordDirty {
		return nil
	}
	if !force && record.pendingProgressFlush < nativeMetadataFlushItems && time.Since(record.progressFlushedAt) < nativeMetadataFlushInterval {
		return nil
	}
	var failures []error
	if record.catalogDirty {
		if err := engine.writeCatalogDiskLocked(); err != nil {
			failures = append(failures, err)
		} else {
			record.catalogDirty = false
			record.catalogFlushCount++
		}
	}
	if record.recordDirty {
		if err := engine.saveSourceRecordsLocked(); err != nil {
			failures = append(failures, err)
		} else {
			record.recordDirty = false
		}
	}
	record.pendingProgressFlush, record.progressFlushedAt = 0, time.Now()
	engine.sourceRecords[source] = record
	return errors.Join(failures...)
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
	if err := engine.saveSourceRecordsLocked(); err != nil {
		delete(engine.sourceTasks, source)
		previous.Running = false
		previous.NeedsSave = true
		engine.sourceRecords[source] = previous
		cancel()
		engine.mu.Unlock()
		return nativeSourceStatus{}, err
	}
	if operation == "update" {
		engine.markSourceCatalogProbeLocked(source)
	}
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
		flushErr := engine.flushSourceProgressLocked(source, true)
		err = errors.Join(err, flushErr)
		record := engine.sourceRecords[source]
		record.Running, record.FinishedAt = false, time.Now()
		record.Stage = "已完成"
		if err != nil {
			var exhausted *nativeSourceRetryExhaustedError
			var retryWindow *nativeSourceRetryWindowError
			switch {
			case stopped && flushErr == nil:
				record.Stage, record.Error = "已停止", "已保留更新内容，可稍后继续"
				record.RetryAt = time.Time{}
			case flushErr == nil && errors.As(err, &retryWindow):
				record.Stage, record.Error = "连续失败已暂停", retryWindow.Error()
				record.RetryAt = time.Time{}
			case flushErr == nil && errors.As(err, &exhausted):
				record.Stage, record.Error = "多次重试仍未成功", publicError(exhausted).Error()
				record.RetryAt = time.Time{}
			default:
				record.Stage, record.Error = "未完成", publicError(err).Error()
				var persistence *nativePersistenceError
				if errors.As(err, &persistence) {
					record.NeedsSave, record.Error = engine.catalogSaveError != nil || engine.sourceSaveError != nil, ""
					record.Stage = "已保存，可继续更新"
					if record.NeedsSave {
						record.Stage = "等待保存"
					}
					break
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
				if isCertificateSourceError(err) {
					record.Stage = "站点证书不受信任"
					record.Error = "站点 TLS 证书不被信任，常见于本机代理（Clash / v2ray 等）替换了证书。请在“网络与资源”里改用直连或换用不做证书替换的代理；也可以打开“允许本机代理替换证书（关闭 TLS 校验）”，该选项有安全风险，仅限你自己的代理。"
					record.RetryAt = time.Time{}
				}
			}
		}
		engine.sourceRecords[source] = record
		if saveErr := engine.saveSourceRecordsLocked(); saveErr != nil {
			record.NeedsSave = true
			record.Stage = "等待保存"
			record.Error = joinNativeWarnings(record.Error, publicError(saveErr).Error())
			engine.sourceRecords[source] = record
		}
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
		if err := engine.retrySourceStep(ctx, source, func(work context.Context) error {
			return engine.loadSourceCatalogPage(work, source, 1)
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
			return fmt.Errorf("目录已加载 %d 页，达到分页范围，更新尚未完成", current.Page)
		}
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
			record.Stage = fmt.Sprintf("加载目录第 %d 页", page)
			record.Completed, record.Total = current.Page, 0
		})
		if err := engine.retrySourceStep(ctx, source, func(work context.Context) error {
			return engine.loadSourceCatalogPage(work, source, page)
		}); err != nil {
			if page > 1 && sourceCatalogPageMissing(err) {
				engine.markSourceCatalogComplete(source)
				note = fmt.Sprintf("目录已加载完，共 %d 页（末页返回 404）", current.Page)
				engine.changeSourceRecord(source, func(record *nativeSourceRecord) { record.Stage = note })
				break
			}
			return err
		}
		next := engine.nativeCached(source)
		if next.HasMore && len(next.Items) <= len(current.Items) {
			stalledPages++
			if stalledPages >= 3 {
				return errors.New("源站目录连续多页没有新增内容，更新尚未完成，已保留当前目录位置")
			}
		} else {
			stalledPages = 0
		}
	}
	attempts := 0
	runTotal := 0
	runCompleted := 0
	window := engine.newSourceRetryWindow()
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
			if err := window.wait(ctx, nextRetry); err != nil {
				return err
			}
			engine.changeSourceRecord(source, func(record *nativeSourceRecord) { record.RetryAt = time.Time{} })
			continue
		}
		runCompleted = sourceRunProgress(runTotal, remaining, runCompleted)
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
			record.Total, record.Completed = runTotal, runCompleted
		})
		work, cancel, budgetErr := window.requestContext(ctx)
		if budgetErr != nil {
			return budgetErr
		}
		work = context.WithValue(work, sourceRetryWindowKey{}, window)
		err := engine.updateSource(work, source, "continuousMetadata")
		deadlineReached := errors.Is(work.Err(), context.DeadlineExceeded) && ctx.Err() == nil
		cancel()
		if deadlineReached {
			return &nativeSourceRetryWindowError{cause: errors.Join(window.cause, err)}
		}
		if err == nil {
			attempts = 0
			window.reset()
			continue
		}
		attempts++
		window.failed(err)
		retry, temporary := engine.sourceRetryWait(err, attempts)
		if !temporary {
			if engine.sourceRetryExhausted(err, attempts) {
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
		if waitErr := window.wait(ctx, retry); waitErr != nil {
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

func (engine *nativeEngine) retrySourceStep(ctx context.Context, source string, step func(context.Context) error) error {
	attempts := 0
	window := engine.newSourceRetryWindow()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		work, cancel, budgetErr := window.requestContext(ctx)
		if budgetErr != nil {
			return budgetErr
		}
		err := step(work)
		deadlineReached := errors.Is(work.Err(), context.DeadlineExceeded) && ctx.Err() == nil
		cancel()
		if deadlineReached {
			return &nativeSourceRetryWindowError{cause: errors.Join(window.cause, err)}
		}
		if err == nil {
			return nil
		}
		attempts++
		window.failed(err)
		retry, temporary := engine.sourceRetryWait(err, attempts)
		if !temporary {
			if engine.sourceRetryExhausted(err, attempts) {
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
		if err := window.wait(ctx, retry); err != nil {
			return err
		}
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) { record.RetryAt = time.Time{} })
	}
}

func (engine *nativeEngine) sourceRetryWait(err error, attempts int) (time.Time, bool) {
	retry, temporary := engine.sourceRetryDelay(err)
	if !temporary {
		return time.Time{}, false
	}
	if attempts >= nativeSourceRetryAttemptLimit && !sourceRetryUnlimited(err) {
		return time.Time{}, false
	}
	var backoff *requestBackoff
	if errors.As(err, &backoff) {
		return retry, true
	}
	if attempts > 0 {
		boundedAttempts := min(attempts, 7)
		retry = engine.sourceNow().Add(time.Duration(min(120, 3*boundedAttempts*boundedAttempts)) * time.Second)
	}
	return retry, true
}

func (engine *nativeEngine) sourceRetryExhausted(err error, attempts int) bool {
	if attempts < nativeSourceRetryAttemptLimit || sourceRetryUnlimited(err) {
		return false
	}
	_, temporary := engine.sourceRetryDelay(err)
	return temporary
}

func sourceRetryUnlimited(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !sourceRetryUnlimited(child) {
				return false
			}
		}
		return true
	}
	if backoff, ok := err.(*requestBackoff); ok {
		return !backoff.verify && (backoff.status == http.StatusTooManyRequests || backoff.status == http.StatusRequestTimeout || backoff.status >= 500)
	}
	if transient, ok := err.(*sourceTransientError); ok {
		return transient.status == http.StatusTooManyRequests || transient.status == http.StatusRequestTimeout || transient.status >= 500
	}
	if network, ok := err.(net.Error); ok {
		return network.Timeout()
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return sourceRetryUnlimited(wrapped.Unwrap())
	}
	return errors.Is(err, context.DeadlineExceeded)
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

func isCertificateSourceError(err error) bool {
	if err == nil {
		return false
	}
	var verification *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	if errors.As(err, &verification) || errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &invalid) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "failed to verify certificate") ||
		strings.Contains(text, "certificate signed by unknown authority") ||
		strings.Contains(text, "x509: certificate")
}

func sourceTerminalError(err error) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if sourceTerminalError(child) {
				return true
			}
		}
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		if sourceTerminalError(wrapped.Unwrap()) {
			return true
		}
	}
	var verification *requestBackoff
	if errors.As(err, &verification) && verification.verify {
		return true
	}
	return isHuangdouAccessError(err) || isPermanentSourceError(err)
}

func (engine *nativeEngine) sourceRetryDelay(err error) (time.Time, bool) {
	if errors.Is(err, context.Canceled) || sourceTerminalError(err) {
		return time.Time{}, false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return time.Time{}, false
		}
		var latest time.Time
		for _, child := range children {
			until, temporary := engine.sourceRetryDelay(child)
			if !temporary {
				return time.Time{}, false
			}
			if until.After(latest) {
				latest = until
			}
		}
		return latest, true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		if _, network := err.(net.Error); !network {
			return engine.sourceRetryDelay(wrapped.Unwrap())
		}
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
		return engine.sourceNow().Add(delay), true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return engine.sourceNow().Add(3 * time.Second), true
	}
	var network net.Error
	if errors.As(err, &network) {
		return engine.sourceNow().Add(3 * time.Second), true
	}
	return time.Time{}, false
}

func waitSourceRetry(ctx context.Context, until time.Time) error {
	return waitSourceRetryAt(ctx, until, time.Now)
}

func waitSourceRetryAt(ctx context.Context, until time.Time, now func() time.Time) error {
	delay := until.Sub(now())
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

func (engine *nativeEngine) updateSource(ctx context.Context, source, operation string) (resultErr error) {
	before := engine.nativeCached(source)
	count := len(before.Items)
	defer func() {
		after := len(engine.nativeCached(source).Items)
		flushErr := engine.changeSourceRecordDeferred(source, func(record *nativeSourceRecord) { record.Added = max(0, after-count) })
		resultErr = errors.Join(resultErr, flushErr)
	}()
	if operation == "continuousMetadata" && nativeMetadataDeferDetailWrite {
		ctx = context.WithValue(ctx, nativeDeferredDetailKey{}, true)
		ctx = context.WithValue(ctx, nativeMetadataFetchKey{}, true)
	}
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
	batchSize := 8
	if operation == "continuousMetadata" {
		batchSize = 64
	}
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
		if len(pending) >= batchSize {
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
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int, len(pending))
	results := make(chan sourceMetadataFetch, len(pending))
	for index := range pending {
		jobs <- index
	}
	close(jobs)
	var workers sync.WaitGroup
	for worker := 0; worker < min(max(1, nativeMetadataWorkers), len(pending)); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				if work.Err() != nil {
					return
				}
				results <- engine.fetchSourceMetadata(work, pending[index], index)
			}
		}()
	}
	go func() {
		workers.Wait()
		close(results)
	}()
	engine.mu.Lock()
	locations := engine.sourceMetadataLocationsLocked(source)
	engine.mu.Unlock()
	var failures []error
	for item := range results {
		drama := pending[item.index]
		err := item.err
		if errors.Is(err, context.Canceled) {
			continue
		}
		engine.mu.Lock()
		if item.fresh.ID != "" {
			engine.applySourceMetadataLocked(source, item.fresh, locations)
			engine.markSourceCatalogDirtyLocked(source)
		}
		record := engine.sourceRecords[source]
		record.MetadataCursor = drama.ID
		now := time.Now()
		checked := err == nil || sourceMetadataMissing(err)
		if checked {
			if record.MetadataChecked == nil {
				record.MetadataChecked = map[string]time.Time{}
			}
			record.MetadataChecked[drama.ID] = now
			delete(record.MetadataRetryAt, drama.ID)
			if operation == "continuousMetadata" {
				record.Completed = min(record.Total, record.Completed+1)
			}
			if window, _ := ctx.Value(sourceRetryWindowKey{}).(*nativeSourceRetryWindow); window != nil {
				window.reset()
			}
		} else if ctx.Err() == nil {
			if record.MetadataRetryAt == nil {
				record.MetadataRetryAt = map[string]time.Time{}
			}
			retry, temporary := engine.sourceRetryDelay(err)
			if !temporary {
				retry = now.Add(5 * time.Minute)
			}
			record.MetadataRetryAt[drama.ID] = retry
		}
		if operation != "continuousMetadata" {
			record.Completed++
		}
		engine.sourceRecords[source] = record
		engine.markSourceRecordDirtyLocked(source)
		flushErr := engine.flushSourceProgressLocked(source, false)
		engine.mu.Unlock()
		if flushErr != nil {
			failures = append(failures, flushErr)
			cancel()
		}
		if err != nil && !sourceMetadataMissing(err) {
			failures = append(failures, err)
			var backoff *requestBackoff
			if _, temporary := engine.sourceRetryDelay(err); !temporary || errors.As(err, &backoff) {
				cancel()
			}
		}
	}
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), errors.Join(failures...))
	}
	if operation != "continuousMetadata" {
		if err := engine.flushSourceProgress(source, true); err != nil {
			return errors.Join(err, errors.Join(failures...))
		}
	}
	return errors.Join(failures...)
}

type sourceMetadataFetch struct {
	index int
	fresh nativeDrama
	err   error
}

func (engine *nativeEngine) fetchSourceMetadata(ctx context.Context, drama nativeDrama, index int) (item sourceMetadataFetch) {
	item.index = index
	defer func() {
		if recover() != nil {
			item.err = errors.New("站源资料解析失败，已保留更新进度")
		}
	}()
	result, err := engine.nativeDetail(ctx, drama)
	if err != nil {
		item.err = err
		return item
	}
	item.fresh = result.(map[string]any)["drama"].(nativeDrama)
	if nativeNeedsExtraMetadata(item.fresh) {
		item.fresh, item.err = engine.nativeExtraMetadata(ctx, item.fresh)
	}
	return item
}

type sourceMetadataLocation struct {
	key   string
	index int
}

func (engine *nativeEngine) sourceMetadataLocationsLocked(source string) map[string][]sourceMetadataLocation {
	locations := map[string][]sourceMetadataLocation{}
	for key, items := range engine.catalogs {
		if key != source && !strings.HasPrefix(key, source+"|") {
			continue
		}
		for index, drama := range items {
			locations[drama.ID] = append(locations[drama.ID], sourceMetadataLocation{key, index})
		}
	}
	return locations
}

func (engine *nativeEngine) applySourceMetadataLocked(source string, fresh nativeDrama, locations map[string][]sourceMetadataLocation) {
	found := false
	for _, location := range locations[fresh.ID] {
		items := engine.catalogs[location.key]
		if location.index >= len(items) || items[location.index].ID != fresh.ID {
			for index := range items {
				if items[index].ID == fresh.ID {
					items[index] = mergeNativeDrama(items[index], fresh)
					found = found || location.key == source
					break
				}
			}
			continue
		}
		items[location.index] = mergeNativeDrama(items[location.index], fresh)
		found = found || location.key == source
	}
	if !found {
		engine.catalogs[source] = mergeNativeCatalog(engine.catalogs[source], []nativeDrama{fresh})
		locations[fresh.ID] = append(locations[fresh.ID], sourceMetadataLocation{source, len(engine.catalogs[source]) - 1})
	}
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

func sourceMetadataMissing(err error) bool {
	return sourceCatalogPageMissing(err)
}

func sourceCatalogPageMissing(err error) bool {
	if err == nil {
		return false
	}
	if _, intercepted := err.(*proxyInterceptionError); intercepted {
		return false
	}
	if _, backoff := err.(*requestBackoff); backoff {
		return false
	}
	if transient, ok := err.(*sourceTransientError); ok {
		return transient.status == http.StatusNotFound || transient.status == http.StatusGone
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !sourceCatalogPageMissing(child) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return sourceCatalogPageMissing(wrapped.Unwrap())
	}
	message := err.Error()
	return strings.Contains(message, "HTTP 404") || strings.Contains(message, "HTTP 410")
}

func (engine *nativeEngine) markSourceCatalogComplete(source string) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	engine.markSourceCatalogHasMoreLocked(source, false)
}

func (engine *nativeEngine) markSourceCatalogProbeLocked(source string) {
	engine.markSourceCatalogHasMoreLocked(source, true)
}

func (engine *nativeEngine) markSourceCatalogHasMoreLocked(source string, hasMore bool) {
	key := nativeCatalogKey(canonicalProviderSource(source), "")
	if engine.catalogStates == nil {
		engine.catalogStates = map[string]nativeCatalogState{}
	}
	state := engine.catalogStates[key]
	state.HasMore = hasMore
	engine.catalogStates[key] = state
	engine.markSourceCatalogDirtyLocked(source)
}
