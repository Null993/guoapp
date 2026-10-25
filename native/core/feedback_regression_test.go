package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHongguoTotalCountWinsOverPreview(t *testing.T) {
	raw := hongguoDramaFromAny(map[string]any{"series_id": "123456", "series_name": "合成剧", "episode_cnt": 3, "episode_total_cnt": 82}, "")
	if nativeNormalize(raw).Episodes != 82 {
		t.Fatalf("preview overwrote total: %+v", raw)
	}
}

func TestGuipianPlaylistAcceptsAttributeVariationsAndIsolatesLines(t *testing.T) {
	body := `<li data-target='#ewave-playlist-1' class='swiper-slide ewave-tab'>线路一</li><li class="swiper-slide ewave-tab" data-target="#ewave-playlist-2">线路二</li><section><div id='ewave-playlist-1'><ul><li class="ep"><a href="/play/1-1-1.html" title="第1集">播放</a></li><li><a href="https://evil.test/play/1-1-2.html" title="第2集">坏地址</a></li></ul></div><div id="ewave-playlist-2"><ul><li><a title='第8集' href='/play/2-2-8.html'>播放</a></li></ul></div></section>`
	_, chapters, err := guipianPickPlaylist(body)
	if err != nil || len(chapters) != 1 || !strings.Contains(chapters[0].PageURL, "/play/1-1-1.html") {
		t.Fatalf("playlist = %+v, %v", chapters, err)
	}
}

func TestHanxiaoquanFullLastPageHasNoMore(t *testing.T) {
	if got := hanxiaoquanPageCount(`<a href="/hxq/a-2.html">2</a><a href="/hxq/a-3.html">3</a>`, "a", 3, hanxiaoquanPageSize); got != 3 {
		t.Fatal(got)
	}
}

func TestContinuousCatalogMayRevisitCachedPages(t *testing.T) {
	calls := 0
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		calls++
		page := request.URL.Query().Get("page")
		if page == "5" {
			return sourceFixtureResponse(request, 200, `<html></html>`), nil
		}
		var body strings.Builder
		for i := 1; i <= 20; i++ {
			fmt.Fprintf(&body, `<article class="video-card"><a href="/series/fixture%d" title="合成%d"></a></article>`, i, i)
		}
		return sourceFixtureResponse(request, 200, body.String()), nil
	})
	for i := 1; i <= 20; i++ {
		id := fmt.Sprintf("huangguo-video:series/fixture%d", i)
		engine.catalogs[sourceHuangguoVideo] = append(engine.catalogs[sourceHuangguoVideo], nativeDrama{ID: id, Source: sourceHuangguoVideo, Title: fmt.Sprintf("合成%d", i)})
		engine.changeSourceRecord(sourceHuangguoVideo, func(record *nativeSourceRecord) {
			if record.MetadataChecked == nil {
				record.MetadataChecked = map[string]time.Time{}
			}
			record.MetadataChecked[id] = time.Now()
		})
	}
	if err := engine.runContinuousSourceUpdate(context.Background(), sourceHuangguoVideo, false); err != nil {
		t.Fatal(err)
	}
	if calls != 5 {
		t.Fatalf("catalog stopped before last page: calls=%d", calls)
	}
}

func TestHongguoRecommendationWebFallbackUsesIndependentCursor(t *testing.T) {
	var paths []string
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path+"?"+request.URL.RawQuery)
		if !strings.HasPrefix(request.URL.Path, "/category/") {
			return sourceFixtureResponse(request, 200, ""), nil
		}
		payload, _ := json.Marshal(map[string]any{"loaderData": map[string]any{"category_page": map[string]any{"isSuccess": true, "recommendList": []any{map[string]any{"series_id": "123456", "series_name": "合成漫剧", "episode_cnt": 3, "episode_total_cnt": 82}}, "pagination": map[string]any{"totalPages": 3}}}})
		return sourceFixtureResponse(request, 200, "<script>window._ROUTER_DATA="+string(payload)+"</script>"), nil
	})
	page, err := d.fetchHongguoRecommendations(context.Background(), hongguoRecommendationQuery{Genre: "comic_series", Offset: 18})
	if err != nil || len(page.Dramas) != 1 || page.NextOffset != 1 || page.SessionID != "web:comic_series" || !page.HasMore {
		t.Fatalf("fallback: %+v, %v", page, err)
	}
	if !strings.Contains(paths[len(paths)-1], "comic-drama?page=1") {
		t.Fatal(paths)
	}
	before := len(paths)
	_, err = d.fetchHongguoRecommendations(context.Background(), hongguoRecommendationQuery{Genre: "comic_series", Offset: page.NextOffset, SessionID: page.SessionID})
	if err != nil || len(paths) != before+1 || !strings.Contains(paths[len(paths)-1], "comic-drama?page=2") {
		t.Fatal(paths, err)
	}
}

func TestHongguoWebRecommendationSkipsSeenPagesWithoutLosingCursor(t *testing.T) {
	var pages []string
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		page := request.URL.Query().Get("page")
		pages = append(pages, page)
		id := "123456"
		if page == "3" {
			id = "123457"
		}
		payload, _ := json.Marshal(map[string]any{"loaderData": map[string]any{"category_page": map[string]any{"isSuccess": true, "recommendList": []any{map[string]any{"series_id": id, "series_name": "合成剧"}}, "pagination": map[string]any{"totalPages": 5}}}})
		return sourceFixtureResponse(request, 200, "<script>window._ROUTER_DATA="+string(payload)+"</script>"), nil
	})
	result, err := d.hongguoWebRecommendations(context.Background(), hongguoRecommendationQuery{Genre: "short_play", Seen: []string{"123456"}})
	if err != nil || len(result.Dramas) != 1 || result.NextOffset != 3 || strings.Join(pages, ",") != "1,2,3" {
		t.Fatal(result, pages, err)
	}
}

func TestNativeWebRecommendationPersistsCursorAcrossDuplicateBatch(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(request.URL.Path, "/category/") {
			return sourceFixtureResponse(request, 200, ""), nil
		}
		id := "123456"
		if request.URL.Query().Get("page") == "5" {
			id = "123457"
		}
		payload, _ := json.Marshal(map[string]any{"loaderData": map[string]any{"category_page": map[string]any{"isSuccess": true, "recommendList": []any{map[string]any{"series_id": id, "series_name": "合成剧"}}, "pagination": map[string]any{"totalPages": 5}}}})
		return sourceFixtureResponse(request, 200, "<script>window._ROUTER_DATA="+string(payload)+"</script>"), nil
	})
	input := nativeInput{Category: "short_play", Force: true}
	if result, err := engine.nativeRecommendations(context.Background(), input); err != nil || len(result.Items) != 1 {
		t.Fatal(result, err)
	}
	input.Force = false
	input.Command = "more"
	if result, err := engine.nativeRecommendations(context.Background(), input); err != nil || !result.HasMore || engine.recommendations["short_play"].Query.Offset != 4 {
		t.Fatal(result, err)
	}
	if result, err := engine.nativeRecommendations(context.Background(), input); err != nil || len(result.Items) != 2 || result.HasMore {
		t.Fatal(result, err)
	}
}
