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
		{"joined 404 and 503", joined, false},
		{"joined 404 and certificate", errors.Join(fmt.Errorf("AI 短剧: %w", notFound), certificate), false},
		{"retry exhausted wrapping 404", newSourceRetryExhaustedError(joined, 8), false},
		{"all missing", errors.Join(notFound, fmt.Errorf("category: %w", errors.New("HTTP 410"))), true},
		{"joined without 404", errors.Join(fmt.Errorf("a: %w", certificate), &sourceTransientError{message: "b HTTP 503", status: http.StatusServiceUnavailable}), false},
		{"plain backoff", &requestBackoff{host: "huangguoai.com", status: http.StatusForbidden, until: time.Now().Add(time.Minute)}, false},
		{"plain server error", errors.New("huangguoai.com HTTP 500"), false},
		{"typed aggregation all missing", &huangguoAICatalogError{failures: []error{notFound, fmt.Errorf("AI 漫剧: %w", errors.New("HTTP 410"))}}, true},
		{"typed aggregation mixed", &huangguoAICatalogError{failures: []error{notFound, fmt.Errorf("AI 漫剧: %w", certificate)}}, false},
	}
	for _, item := range cases {
		if got := sourceCatalogPageMissing(item.err); got != item.want {
			t.Fatalf("%s: sourceCatalogPageMissing = %v, want %v (%v)", item.name, got, item.want, item.err)
		}
	}
}

func huangguoAICategoryTransport(failing map[string]bool) sourceFixtureTransport {
	return func(request *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(request.URL.Path, "/api/videos/category/") {
			return sourceFixtureResponse(request, http.StatusNotFound, "not found"), nil
		}
		id := strings.TrimPrefix(request.URL.Path, "/api/videos/category/")
		if failing[id] {
			return sourceFixtureResponse(request, http.StatusNotFound, "not found"), nil
		}
		for index, category := range nativeAICategories {
			if category.ID == id {
				body := fmt.Sprintf(`{"data":{"items":[{"id":"7%03d","title":"合成%s","description":"完整资料","episode_count":6}]}}`, index, category.Name)
				return sourceFixtureResponse(request, http.StatusOK, body), nil
			}
		}
		return sourceFixtureResponse(request, http.StatusNotFound, "not found"), nil
	}
}

func TestHuangguoAICatalogWarnsOnlyForPrimaryCategory(t *testing.T) {
	primary := nativeAIPrimaryCategoryID()
	if primary == "" || primary != nativeAICategories[0].ID {
		t.Fatalf("主分类应取首个 AI 分类：%q", primary)
	}
	if len(nativeAICategories) < 2 {
		t.Fatal("需要至少一个次要分类")
	}
	secondary := nativeAICategories[1].ID
	failingAll := map[string]bool{}
	for _, category := range nativeAICategories {
		failingAll[category.ID] = true
	}
	cases := []struct {
		name        string
		failing     map[string]bool
		wantWarning bool
		wantFresh   bool
		wantErr     bool
		wantItems   int
	}{
		{name: "primary fails", failing: map[string]bool{primary: true}, wantWarning: true, wantItems: len(nativeAICategories) - 1},
		{name: "only secondary fails", failing: map[string]bool{secondary: true}, wantFresh: true, wantItems: len(nativeAICategories) - 1},
		{name: "all fail", failing: failingAll, wantErr: true},
	}
	for _, item := range cases {
		engine := sourceFixtureEngine(t, huangguoAICategoryTransport(item.failing))
		engine.downloader.cfg.HuangguoAIURL = "https://huangguoai.test"
		result, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceHuangguoAI, Page: 1})
		if item.wantErr {
			if err == nil {
				t.Fatalf("%s: 全部分类失败必须告警：%+v", item.name, result)
			}
			var aggregated *huangguoAICatalogError
			if !errors.As(err, &aggregated) || aggregated.primary == nil {
				t.Fatalf("%s: 全部分类失败时主分类错误应保留：%v", item.name, err)
			}
			if !strings.Contains(err.Error(), "HTTP 404") {
				t.Fatalf("%s: 错误文案应含分类失败原因：%v", item.name, err)
			}
			if result.Fresh {
				t.Fatalf("%s: 全部分类失败不应标记为最新", item.name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: 不应报错：%v", item.name, err)
		}
		if len(result.Items) != item.wantItems {
			t.Fatalf("%s: 条目数 = %d，期望 %d", item.name, len(result.Items), item.wantItems)
		}
		if (result.Warning != "") != item.wantWarning {
			t.Fatalf("%s: Warning = %q，期望告警 = %v", item.name, result.Warning, item.wantWarning)
		}
		if result.Fresh != item.wantFresh {
			t.Fatalf("%s: Fresh = %v，期望 %v", item.name, result.Fresh, item.wantFresh)
		}
		if result.catalogError == nil {
			t.Fatalf("%s: 分类失败必须被记录（不吞掉）", item.name)
		}
	}
}

func TestHuangguoAISecondaryFailureStaysRecorded(t *testing.T) {
	engine := sourceFixtureEngine(t, huangguoAICategoryTransport(map[string]bool{nativeAICategories[1].ID: true}))
	engine.downloader.cfg.HuangguoAIURL = "https://huangguoai.test"
	result, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceHuangguoAI, Page: 1})
	if err != nil || result.Warning != "" || !result.Fresh {
		t.Fatalf("次要分类失败不应告警或影响最新标记：err=%v warning=%q fresh=%v", err, result.Warning, result.Fresh)
	}
	var aggregated *huangguoAICatalogError
	if !errors.As(result.catalogError, &aggregated) || aggregated.primary != nil {
		t.Fatalf("次要分类失败应被记录且不视为主分类失败：%v", result.catalogError)
	}
	if len(aggregated.failures) != 1 {
		t.Fatalf("应记录 1 个次要失败，实际 %d", len(aggregated.failures))
	}
	cached := engine.nativeCached(sourceHuangguoAI)
	if !cached.Fresh || cached.Warning != "" {
		t.Fatalf("缓存不应因次要分类失败而失去最新标记：%+v", cached)
	}
}
