package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"
)

func TestHuangjuLogicalPaginationBeyondServerPageCap(t *testing.T) {
	d := providerParityDownloader(t, func(request *http.Request, form url.Values) (*http.Response, error) {
		if request.URL.Path == "/auth/guest" {
			return sourceFixtureResponse(request, 200, `{"token":"synthetic"}`), nil
		}
		page, _ := strconv.Atoi(request.URL.Query().Get("page"))
		if page > 100 || request.URL.Query().Get("pageSize") != "60" {
			t.Fatal("request exceeded server pagination contract", request.URL)
		}
		var rows []map[string]any
		for index := (page - 1) * 60; index < min(page*60, 3170); index++ {
			rows = append(rows, map[string]any{"id": fmt.Sprint(index + 1), "slug": fmt.Sprintf("synthetic-%d", index+1), "title": "合成目录"})
		}
		body, _ := json.Marshal(map[string]any{"items": rows, "page": page, "pageSize": 60, "total": 3170})
		return sourceFixtureResponse(request, 200, string(body)), nil
	})
	for _, page := range []int{100, 101, 158, 159} {
		items, more, err := d.fetchHuangjuCatalogPage(context.Background(), page, "", "")
		if err != nil || len(items) != min(20, 3170-(page-1)*20) || more != (page < 159) {
			t.Fatal(page, len(items), more, err)
		}
		if items[0].SourceID != fmt.Sprintf("synthetic-%d-%d", (page-1)*20+1, (page-1)*20+1) {
			t.Fatal("logical page skipped or duplicated rows", page, items[0].SourceID)
		}
	}
}

func TestGuipianEmptyPlaylistCanRefreshMetadata(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, 200, `<h1 class="media-title">合成待播作品</h1><li data-target="#ewave-playlist-1">线路</li><div id="ewave-playlist-1"><ul></ul></div>`), nil
	})
	drama := nativeDrama{ID: "guipian:123", Source: sourceGuipian, Title: "合成待播作品"}
	if _, err := engine.nativeDetail(context.Background(), drama); err == nil {
		t.Fatal("empty playlist counted as playable detail")
	}
	ctx := context.WithValue(context.Background(), nativeMetadataFetchKey{}, true)
	if _, err := engine.nativeDetail(ctx, drama); err != nil {
		t.Fatal("valid metadata for an unreleased work interrupted update", err)
	}
}
