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
	err := engine.retrySourceStep(context.Background(), sourceYeguo, func() error {
		attempts++
		return &requestBackoff{host: "analyze.buxefaex.cc", status: http.StatusServiceUnavailable, until: time.Now().Add(-time.Second)}
	})
	if attempts != 2 {
		t.Fatalf("retry loop stopped after %d attempts, want 2", attempts)
	}
	if err == nil {
		t.Fatal("retry loop returned no error after reaching the attempt limit")
	}
	if !strings.Contains(err.Error(), "多次重试仍未成功") || !strings.Contains(err.Error(), "analyze.buxefaex.cc HTTP 503") {
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

func TestSourceRetryKeepsAutomaticRetryBeforeLimit(t *testing.T) {
	err := fmt.Errorf("获取剧集资料失败: %w", context.DeadlineExceeded)
	if _, temporary := sourceRetryWait(err, nativeSourceRetryAttemptLimit-1); !temporary {
		t.Fatal("recoverable failure stopped retrying before the attempt limit")
	}
	if _, temporary := sourceRetryWait(err, nativeSourceRetryAttemptLimit); temporary {
		t.Fatal("recoverable failure kept retrying past the attempt limit")
	}
	if !sourceRetryExhausted(err, nativeSourceRetryAttemptLimit) || sourceRetryExhausted(err, nativeSourceRetryAttemptLimit-1) {
		t.Fatal("attempt limit boundary is inconsistent")
	}
}

func TestSourceRetryNeverRepeatsTerminalFailures(t *testing.T) {
	verify := &requestBackoff{host: "huangguo.video", status: http.StatusForbidden, until: time.Now().Add(time.Minute), verify: true}
	denied := &requestBackoff{host: "huangguo.video", status: http.StatusForbidden, until: time.Now().Add(time.Minute)}
	missingHost := &net.DNSError{Err: "no such host", Name: "www.sorani.net", IsNotFound: true}
	for _, err := range []error{verify, denied, missingHost, context.Canceled} {
		if _, temporary := sourceRetryWait(err, 1); temporary {
			t.Fatalf("terminal failure was retried: %v", err)
		}
		if sourceRetryExhausted(err, nativeSourceRetryAttemptLimit) {
			t.Fatalf("terminal failure was reported as exhausted retries: %v", err)
		}
	}
}

func TestSourceTaskStopsAfterBoundedRetries(t *testing.T) {
	previous := nativeSourceRetryAttemptLimit
	nativeSourceRetryAttemptLimit = 2
	defer func() { nativeSourceRetryAttemptLimit = previous }()
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
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
	if !strings.Contains(status.Error, "context deadline exceeded") || !strings.Contains(status.Error, "已重试 2 次") {
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
