package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func hongguoWebDetailFixture(id string, count int) string {
	row := map[string]any{"series_id": id, "series_name": "合成详情", "episode_cnt": 3, "episode_total_cnt": count, "vid_list": []string{"800001", "800002", "800003"}}
	data, _ := json.Marshal(map[string]any{"loaderData": map[string]any{"detail_page": map[string]any{"seriesDetail": row}}})
	return "<script>window._ROUTER_DATA=" + string(data) + "</script>"
}

func TestHongguoRankingPreviewIDsAreNotTotalCount(t *testing.T) {
	board, _ := findRankingBoard("hongguo-hot")
	rows := rankingRows(1)
	rows[0]["episodeVids"] = []string{"800001", "800002", "800003"}
	page, err := parseHongguoRanking(rankingFixture(board, 1, rows), board, 1)
	if err != nil || nativeNormalize(page.Items[0].Drama).Episodes != 0 {
		t.Fatal(page, err)
	}
}

func TestHongguoWebMetadataKeepsFullCountAndWarmsPlayback(t *testing.T) {
	var requests atomic.Int32
	engine := sourceFixtureEngine(t, func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		if r.URL.Path != "/detail" {
			return nil, fmt.Errorf("unexpected request %s", r.URL.Path)
		}
		return sourceFixtureResponse(r, 200, hongguoWebDetailFixture("700001", 82)), nil
	})
	drama := nativeDrama{ID: "hongguo:700001", Source: sourceHongguo, Episodes: 3}
	fresh, err := engine.nativeExtraMetadata(context.Background(), drama)
	if err != nil || fresh.Episodes != 82 {
		t.Fatal(fresh, err)
	}
	result, err := engine.nativeDetail(context.Background(), drama)
	if err != nil {
		t.Fatal(err)
	}
	resolved := result.(map[string]any)
	if resolved["drama"].(nativeDrama).Episodes != 82 || len(resolved["chapters"].([]Chapter)) != 3 || requests.Load() != 1 || resolved["warning"] == "" {
		t.Fatal(resolved, requests.Load())
	}
}

func TestHongguoPlaybackWebHedgeDoesNotWaitForStalledApp(t *testing.T) {
	canceled := make(chan struct{})
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/detail" {
			return sourceFixtureResponse(r, 200, hongguoWebDetailFixture("700001", 82)), nil
		}
		<-r.Context().Done()
		select {
		case <-canceled:
		default:
			close(canceled)
		}
		return nil, r.Context().Err()
	})
	started := time.Now()
	entry, err := d.hongguoPlaybackDetail(context.Background(), "700001")
	if err != nil || len(entry.Chapters) != 3 || time.Since(started) > 2*time.Second {
		t.Fatal(entry, err, time.Since(started))
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("losing App request not canceled")
	}
}

func TestHongguoPlaybackSharesConcurrentWebRequest(t *testing.T) {
	var webs atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/detail" {
			return sourceFixtureResponse(r, 200, ""), nil
		}
		if webs.Add(1) == 1 {
			close(entered)
		}
		<-release
		return sourceFixtureResponse(r, 200, hongguoWebDetailFixture("700001", 82)), nil
	})
	results := make(chan error, 2)
	go func() { _, err := d.hongguoWebDetail(context.Background(), "700001"); results <- err }()
	<-entered
	go func() { _, err := d.hongguoPlaybackDetail(context.Background(), "700001"); results <- err }()
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if webs.Load() != 1 {
		t.Fatal("duplicate web requests", webs.Load())
	}
}

func TestNativeReadIdleSlotsDoNotBlockMoreThan128Titles(t *testing.T) {
	engine := sourceFixtureEngine(t, nil)
	for i := 0; i < 300; i++ {
		_, finish, err := engine.beginRead(context.Background(), nativeInput{Session: fmt.Sprintf("fixture:metadata-%d", i), Sequence: 1})
		if err != nil {
			t.Fatalf("title %d: %v", i, err)
		}
		finish()
	}
	if len(engine.reads) > 128 || engine.readCount != 0 {
		t.Fatal(len(engine.reads), engine.readCount)
	}
}

func TestAutoEmpty404AndResetRemainNetworkFailure(t *testing.T) {
	transport := autoNetworkFixture(t, func(r *http.Request, proxy *url.URL) (*http.Response, error) {
		if proxy != nil {
			time.Sleep(2 * time.Millisecond)
			return autoNetworkResponse(r, 404, ""), nil
		}
		return nil, autoNetworkDialError()
	})
	request, _ := http.NewRequest("GET", "https://huangguo.video/series/fixture", nil)
	_, err := transport.RoundTrip(request)
	var intercepted *proxyInterceptionError
	if err == nil || !errors.As(err, &intercepted) || sourceMetadataMissing(err) || !strings.Contains(err.Error(), "直连") || !strings.Contains(err.Error(), "代理") {
		t.Fatal(err)
	}
}

func TestHongguoUnconfirmedPreviewCountIsNotReturnedAsReliableMetadata(t *testing.T) {
	for _, count := range []int{0, 3} {
		engine := sourceFixtureEngine(t, func(r *http.Request) (*http.Response, error) {
			return sourceFixtureResponse(r, 200, hongguoWebDetailFixture("700001", count)), nil
		})
		_, err := engine.nativeMetadata(context.Background(), nativeDrama{ID: "hongguo:700001", Source: sourceHongguo, Episodes: 3})
		if count == 0 && err == nil {
			t.Fatal("unconfirmed preview total accepted")
		}
		if count == 3 && err != nil {
			t.Fatal("confirmed three-episode work rejected", err)
		}
	}
}

func TestNativeRankingOldPreviewCacheCannotOverwriteKnownTotal(t *testing.T) {
	engine := sourceFixtureEngine(t, nil)
	id := "hongguo:700001"
	engine.catalogs[sourceHongguo] = []nativeDrama{{ID: id, Source: sourceHongguo, Episodes: 82}}
	engine.downloader.rankings.pages["hongguo-hot:1"] = rankingPage{BoardID: "hongguo-hot", Page: 1, FetchedAt: time.Now(), Items: []rankingItem{{Rank: 1, Drama: Drama{ID: id, Source: sourceHongguo, Title: "合成剧", TotalEpisode: 3}}}}
	result, err := engine.nativeRanking(context.Background(), nativeInput{Board: "hongguo-hot", Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	var page struct {
		Items []struct {
			Drama nativeDrama `json:"drama"`
		} `json:"items"`
	}
	if json.Unmarshal(encoded, &page) != nil || len(page.Items) != 1 || page.Items[0].Drama.Episodes != 82 {
		t.Fatal(string(encoded))
	}
}
