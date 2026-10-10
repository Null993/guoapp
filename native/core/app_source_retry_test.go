package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSourceRetryStepStopsAtAttemptLimit(t *testing.T) {
	previous := nativeSourceRetryAttemptLimit
	nativeSourceRetryAttemptLimit = 2
	defer func() { nativeSourceRetryAttemptLimit = previous }()
	engine := sourceFixtureEngine(t, nil)
	attempts := 0
	err := engine.retrySourceStep(context.Background(), sourceYeguo, func(context.Context) error {
		attempts++
		return &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}
	})
	if attempts != 2 {
		t.Fatalf("retry loop stopped after %d attempts, want 2", attempts)
	}
	if err == nil {
		t.Fatal("retry loop returned no error after reaching the attempt limit")
	}
	if !strings.Contains(err.Error(), "多次重试仍未成功") || !strings.Contains(err.Error(), "connection reset by peer") {
		t.Fatalf("exhausted retry lost the real cause: %v", err)
	}
	var exhausted *nativeSourceRetryExhaustedError
	if !errors.As(err, &exhausted) || exhausted.attempts != 2 {
		t.Fatalf("retry exhaustion is not machine readable: %v", err)
	}
	status := engine.sourceStatus(sourceYeguo)
	if status.Stage != "多次重试仍未成功" || !status.RetryAt.IsZero() {
		t.Fatalf("retry exhaustion did not clear the retry pause: %+v", status)
	}
}

func TestSourceRetryKeepsAutomaticRetryPastLimit(t *testing.T) {
	engine := &nativeEngine{}
	failures := []error{
		fmt.Errorf("获取剧集资料失败: %w", context.DeadlineExceeded),
		&requestBackoff{status: http.StatusTooManyRequests, until: time.Now()},
		&sourceTransientError{status: http.StatusRequestTimeout},
		&sourceTransientError{status: http.StatusServiceUnavailable},
	}
	for _, err := range failures {
		if _, temporary := engine.sourceRetryWait(err, nativeSourceRetryAttemptLimit+3); !temporary {
			t.Fatalf("recoverable failure stopped automatic retry: %v", err)
		}
		if engine.sourceRetryExhausted(err, nativeSourceRetryAttemptLimit+3) {
			t.Fatalf("recoverable failure was reported as exhausted: %v", err)
		}
	}
}

func TestSourceRetryNeverRepeatsTerminalFailures(t *testing.T) {
	engine := &nativeEngine{}
	verify := &requestBackoff{host: "huangguo.video", status: http.StatusForbidden, until: time.Now().Add(time.Minute), verify: true}
	denied := &requestBackoff{host: "huangguo.video", status: http.StatusForbidden, until: time.Now().Add(time.Minute)}
	missingHost := &net.DNSError{Err: "no such host", Name: "www.sorani.net", IsNotFound: true}
	for _, err := range []error{verify, denied, missingHost, context.Canceled} {
		if _, temporary := engine.sourceRetryWait(err, 1); temporary {
			t.Fatalf("terminal failure was retried: %v", err)
		}
		if engine.sourceRetryExhausted(err, nativeSourceRetryAttemptLimit) {
			t.Fatalf("terminal failure was reported as exhausted retries: %v", err)
		}
	}
}

func TestSourceTaskStopsAfterBoundedRetries(t *testing.T) {
	previous := nativeSourceRetryAttemptLimit
	nativeSourceRetryAttemptLimit = 2
	defer func() { nativeSourceRetryAttemptLimit = previous }()
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}
	})
	if _, err := engine.startSourceTask(sourceHongguo, "update", nativeDrama{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	status := engine.sourceStatus(sourceHongguo)
	for status.Running && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		status = engine.sourceStatus(sourceHongguo)
	}
	if status.Running {
		t.Fatal("source update never stopped retrying")
	}
	if status.Stage != "多次重试仍未成功" {
		t.Fatalf("bounded retry stage = %q, error = %q", status.Stage, status.Error)
	}
	if !strings.Contains(status.Error, "connection reset by peer") || !strings.Contains(status.Error, "已重试 2 次") {
		t.Fatalf("bounded retry lost the real cause: %q", status.Error)
	}
	if !status.RetryAt.IsZero() {
		t.Fatalf("bounded retry kept a retry pause: %s", status.RetryAt)
	}
}

func TestSourceTaskReportsSiteDenial(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusForbidden, "<html>403</html>"), nil
	})
	if _, err := engine.startSourceTask(sourceHongguo, "update", nativeDrama{}); err != nil {
		t.Fatal(err)
	}
	status := awaitSourceTask(t, engine, sourceHongguo)
	if status.Stage != "站点拒绝访问" {
		t.Fatalf("site denial stage = %q, error = %q", status.Stage, status.Error)
	}
	if !strings.Contains(status.Error, "验证与授权") {
		t.Fatalf("site denial lost its recovery hint: %q", status.Error)
	}
	if !status.RetryAt.After(time.Now()) {
		t.Fatalf("site denial should pause the source instead of retrying: %s", status.RetryAt)
	}
}

func TestSourceTaskReportsRealNetworkFailure(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}
	})
	engine.catalogs[sourceHongguo] = []nativeDrama{{ID: "hongguo:1", Source: sourceHongguo, Title: "合成短剧"}}
	if _, err := engine.startSourceTask(sourceHongguo, "metadata", nativeDrama{}); err != nil {
		t.Fatal(err)
	}
	status := awaitSourceTask(t, engine, sourceHongguo)
	if status.Stage != "网络请求失败" {
		t.Fatalf("network failure stage = %q, error = %q", status.Stage, status.Error)
	}
	if !strings.Contains(status.Error, "connection reset by peer") || !strings.Contains(status.Error, "已保留更新内容") {
		t.Fatalf("network failure lost the real cause: %q", status.Error)
	}
	if !status.RetryAt.IsZero() {
		t.Fatalf("network failure kept a retry pause: %s", status.RetryAt)
	}
}

func TestContinuousMetadataKeepsRunProgress(t *testing.T) {
	engine := sourceFixtureEngine(t, nil)
	engine.catalogs[sourceHuangguoAI] = []nativeDrama{{ID: "huangguoai:1", Source: sourceHuangguoAI, Title: "合成短剧", Description: "完整资料", Episodes: 2}}
	engine.changeSourceRecord(sourceHuangguoAI, func(record *nativeSourceRecord) {
		record.Completed, record.Total = 7, 8
		record.MetadataChecked = map[string]time.Time{"huangguoai:1": time.Now()}
	})
	if err := engine.updateSource(context.Background(), sourceHuangguoAI, "continuousMetadata"); err != nil {
		t.Fatal(err)
	}
	status := engine.sourceStatus(sourceHuangguoAI)
	if status.Completed != 7 || status.Total != 8 {
		t.Fatalf("continuous metadata reset the run progress: %d/%d", status.Completed, status.Total)
	}
	if err := engine.updateSource(context.Background(), sourceHuangguoAI, "metadata"); err != nil {
		t.Fatal(err)
	}
	status = engine.sourceStatus(sourceHuangguoAI)
	if status.Completed != 0 || status.Total != 0 {
		t.Fatalf("manual metadata pass no longer reports its own progress: %d/%d", status.Completed, status.Total)
	}
}

func TestSourceRunProgressIsMonotonic(t *testing.T) {
	progress := 0
	for _, remaining := range []int{8, 6, 1, 0} {
		progress = sourceRunProgress(8, remaining, progress)
		if progress != 8-remaining {
			t.Fatalf("progress for %d remaining = %d", remaining, progress)
		}
	}
	if got := sourceRunProgress(8, 0, 5); got != 8 {
		t.Fatalf("finished run reported %d/8", got)
	}
	if got := sourceRunProgress(8, 12, 3); got != 3 {
		t.Fatalf("shrinking work total moved progress backwards: %d", got)
	}
	if got := sourceRunProgress(8, 4, 6); got != 6 {
		t.Fatalf("progress regressed to %d", got)
	}
}

func TestSourceRetryWindowResetsAfterSuccess(t *testing.T) {
	engine := sourceFixtureEngine(t, nil)
	window := engine.newSourceRetryWindow()
	window.failed(context.DeadlineExceeded)
	start := window.started
	if err := window.wait(context.Background(), start.Add(59*time.Minute)); err != nil {
		t.Fatal(err)
	}
	window.reset()
	window.failed(context.DeadlineExceeded)
	if err := window.wait(context.Background(), window.started.Add(59*time.Minute)); err != nil {
		t.Fatal(err)
	}
	err := window.wait(context.Background(), window.started.Add(time.Hour))
	var exhausted *nativeSourceRetryWindowError
	if !errors.As(err, &exhausted) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("missing retry window cause: %v", err)
	}
}

func TestSourceRetryWindowCanBeStopped(t *testing.T) {
	engine := sourceFixtureEngine(t, nil)
	window := engine.newSourceRetryWindow()
	window.failed(context.DeadlineExceeded)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := window.wait(ctx, time.Now().Add(time.Hour)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel lost: %v", err)
	}
}

func TestSourceRetryWindowBoundsInFlightContext(t *testing.T) {
	engine := &nativeEngine{sourceRetryPolicy: &nativeSourceRetryPolicy{limit: 20 * time.Millisecond}}
	window := engine.newSourceRetryWindow()
	window.failed(context.DeadlineExceeded)
	work, cancel, err := window.requestContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	select {
	case <-work.Done():
		if !errors.Is(work.Err(), context.DeadlineExceeded) {
			t.Fatalf("unexpected request cancellation: %v", work.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("in-flight request was not bounded by the retry window")
	}
}

func TestSourceRetryUsesInjectedClock(t *testing.T) {
	policy := sourceFixtureRetryPolicy()
	engine := &nativeEngine{sourceRetryPolicy: policy}
	retry, temporary := engine.sourceRetryWait(&sourceTransientError{status: http.StatusServiceUnavailable}, 1)
	if !temporary {
		t.Fatal("可恢复失败应可重试")
	}
	if want := policy.now().Add(3 * time.Second); !retry.Equal(want) {
		t.Fatalf("退避时间未使用注入时钟：retry=%s want=%s", retry, want)
	}
	if _, temporary := engine.sourceRetryWait(&sourceTransientError{status: http.StatusNotFound}, nativeSourceRetryAttemptLimit); temporary {
		t.Fatal("达到次数上限的有界故障不应继续自动重试")
	}
}

func TestSourceMetadataSuccessResetsRetryWindow(t *testing.T) {
	previousBuild := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = previousBuild }()

	engine := sourceFixtureEngine(t, metadataDetailTransport(func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusOK, `<h1>合成短剧</h1><meta name="description" content="完整资料"><div data-hls="https://media.example.test/master.m3u8"></div>`), nil
	}))
	engine.downloader.cfg.HuangguoVideoURL = "https://huangguo.video"
	if _, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceHuangguoVideo, Page: 1}); err != nil {
		t.Fatal(err)
	}
	window := engine.newSourceRetryWindow()
	window.failed(errors.New("synthetic failure"))
	ctx := context.WithValue(context.Background(), sourceRetryWindowKey{}, window)
	if err := engine.updateSource(ctx, sourceHuangguoVideo, "continuousMetadata"); err != nil {
		t.Fatal(err)
	}
	if !window.started.IsZero() || window.cause != nil {
		t.Fatalf("资料请求成功后应重置连续失败窗口：started=%s cause=%v", window.started, window.cause)
	}
}
