package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func huangguoAICatalogPayload(items int) string {
	rows := make([]map[string]any, 0, items)
	for index := 0; index < items; index++ {
		rows = append(rows, map[string]any{
			"id":            fmt.Sprintf("90%02d", index),
			"title":         fmt.Sprintf("合成黄果短剧%d", index),
			"description":   "完整资料",
			"episode_count": 6,
		})
	}
	payload, _ := json.Marshal(map[string]any{"data": map[string]any{"items": rows}})
	return string(payload)
}

func huangguoAICatalogTransport(items, tailStatus int) sourceFixtureTransport {
	payload := huangguoAICatalogPayload(items)
	return func(request *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(request.URL.Path, "/api/videos/category/") {
			return sourceFixtureResponse(request, http.StatusNotFound, "not found"), nil
		}
		if request.URL.Query().Get("page") != "1" {
			return sourceFixtureResponse(request, tailStatus, "page out of range"), nil
		}
		return sourceFixtureResponse(request, http.StatusOK, payload), nil
	}
}

func runHuangguoAIUpdateTask(t *testing.T, engine *nativeEngine) {
	t.Helper()
	engine.downloader.cfg.HuangguoAIURL = "https://huangguoai.test"
	engine.runSourceTask(context.Background(), sourceHuangguoAI, "update", nativeDrama{}, &nativeSourceTask{cancel: func() {}})
}

func TestSourceContinuousUpdateEndsOnHuangguoAITail404(t *testing.T) {
	engine := sourceFixtureEngine(t, huangguoAICatalogTransport(24, http.StatusNotFound))
	runHuangguoAIUpdateTask(t, engine)

	status := engine.sourceStatus(sourceHuangguoAI)
	if status.Running || status.Stage != "已完成" || status.Error != "" {
		t.Fatalf("黄果AI 翻页越界 404 应正常结束：stage=%q error=%q", status.Stage, status.Error)
	}
	if status.HasMore {
		t.Fatal("黄果AI 翻页越界 404 后仍标记还有下一页")
	}
	cached := engine.nativeCached(sourceHuangguoAI)
	if cached.HasMore || len(cached.Items) == 0 {
		t.Fatalf("黄果AI 目录进度或内容被破坏：page=%d hasMore=%v items=%d", cached.Page, cached.HasMore, len(cached.Items))
	}
}

func TestSourceContinuousUpdateKeepsHuangguoAIFirstPage404AsError(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, http.StatusNotFound, "not found"), nil
	})
	runHuangguoAIUpdateTask(t, engine)

	status := engine.sourceStatus(sourceHuangguoAI)
	if status.Running || status.Stage != "未完成" || !strings.Contains(status.Error, "HTTP 404") {
		t.Fatalf("黄果AI 首页 404 应仍然报错：stage=%q error=%q", status.Stage, status.Error)
	}
	if !engine.nativeCached(sourceHuangguoAI).HasMore {
		t.Fatal("黄果AI 首页 404 不应被当作目录已加载完")
	}
}

func TestSourceCatalogPageMissingHandlesJoinedProviderFailures(t *testing.T) {
	certificate := errors.New(`Get "https://thu.example.cc/api/videos/category/ai-manju?page=41": tls: failed to verify certificate: x509: certificate signed by unknown authority`)
	notFound := errors.New("huangguoai.com HTTP 404")
	joined := errors.Join(
		fmt.Errorf("AI 短剧: %w", errors.New("huangguoai.com HTTP 404")),
		fmt.Errorf("AI 漫剧: %w", &sourceTransientError{message: "mirror.example.cc HTTP 503", status: http.StatusServiceUnavailable}),
	)
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"joined 404 and 503", joined, true},
		{"joined 404 and certificate", errors.Join(fmt.Errorf("AI 短剧: %w", notFound), certificate), true},
		{"retry exhausted wrapping 404", newSourceRetryExhaustedError(joined, 8), true},
		{"joined without 404", errors.Join(fmt.Errorf("a: %w", certificate), &sourceTransientError{message: "b HTTP 503", status: http.StatusServiceUnavailable}), false},
		{"plain backoff", &requestBackoff{host: "huangguoai.com", status: http.StatusForbidden, until: time.Now().Add(time.Minute)}, false},
		{"plain server error", errors.New("huangguoai.com HTTP 500"), false},
	}
	for _, item := range cases {
		if got := sourceCatalogPageMissing(item.err); got != item.want {
			t.Fatalf("%s: sourceCatalogPageMissing = %v, want %v (%v)", item.name, got, item.want, item.err)
		}
	}
}
