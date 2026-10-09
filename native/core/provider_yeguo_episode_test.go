package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func yeguoDetailFixtureDownloader(t *testing.T, body string) *Downloader {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/playlet/detail" {
			t.Errorf("unexpected route: %s", request.URL.Path)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, body)
	}))
	t.Cleanup(server.Close)
	downloader := &Downloader{}
	downloader.yeguoClient().access = &yeguoAccess{base: server.URL, identifier: "fixture-trace", loadedAt: time.Now()}
	return downloader
}

func TestYeguoDetailRenumbersDuplicateSourceOrder(t *testing.T) {
	body := `{"status":"1","data":{"video_id":"3525","title":"偷窥","episodes":[` +
		`{"id":"187416","sort":1,"title":"第4集《偷窥》冷淡婚姻里的李爱"},` +
		`{"id":"187417","sort":1,"title":"第3集《偷窥》冷淡婚姻里的李爱"},` +
		`{"id":"187418","sort":1,"title":"第2集《偷窥》冷淡婚姻里的李爱"},` +
		`{"id":"187419","sort":1,"title":"第1集《偷窥》冷淡婚姻里的李爱"}]}}`
	downloader := yeguoDetailFixtureDownloader(t, body)
	drama, chapters, err := downloader.fetchYeguoDetail(context.Background(), "3525")
	if err != nil {
		t.Fatalf("duplicate source order must not fail the source: %v", err)
	}
	if drama.SourceID != "3525" || len(chapters) != 4 {
		t.Fatalf("unexpected detail: drama=%+v chapters=%d", drama, len(chapters))
	}
	for index, chapter := range chapters {
		if chapter.EpisodeString(0) != strconv.Itoa(index+1) {
			t.Fatalf("chapter %d number = %s, want %d", index, chapter.EpisodeString(0), index+1)
		}
	}
	if !strings.Contains(chapters[0].Title, "第1集") || !strings.Contains(chapters[3].Title, "第4集") {
		t.Fatalf("titles were not ordered by their own episode numbers: %+v", chapters)
	}
	if !strings.HasSuffix(chapters[0].ID, ":187419") {
		t.Fatalf("source episode id must be preserved: %+v", chapters[0])
	}
}

func TestYeguoDetailKeepsSparseSourceOrderNumbers(t *testing.T) {
	body := `{"status":"1","data":{"video_id":"3380","title":"榨精病栋","episodes":[` +
		`{"id":"187051","sort":1,"title":"榨精病栋  1-4合集"},` +
		`{"id":"187052","sort":5,"title":"《榨精病院》EP5"},` +
		`{"id":"187053","sort":6,"title":"榨精病栋 6"},` +
		`{"id":"187054","sort":7,"title":"《榨精病院》EP7"},` +
		`{"id":"187055","sort":8,"title":"榨精病栋8"}]}}`
	downloader := yeguoDetailFixtureDownloader(t, body)
	_, chapters, err := downloader.fetchYeguoDetail(context.Background(), "3380")
	if err != nil {
		t.Fatalf("sparse source order must not fail the source: %v", err)
	}
	if len(chapters) != 5 {
		t.Fatalf("chapters = %d, want 5: %+v", len(chapters), chapters)
	}
	wantNumbers := []string{"1", "5", "6", "7", "8"}
	for index, chapter := range chapters {
		if chapter.EpisodeString(0) != wantNumbers[index] {
			t.Fatalf("chapter %d number = %s, want %s（本地记录身份必须保持源站编号）", index, chapter.EpisodeString(0), wantNumbers[index])
		}
	}
	if chapters[0].Title != "榨精病栋  1-4合集" || chapters[4].Title != "榨精病栋8" {
		t.Fatalf("source titles must be preserved: %+v", chapters)
	}
	if !strings.HasSuffix(chapters[1].PageURL, "/ep-5/") || !strings.HasSuffix(chapters[4].PageURL, "/ep-8/") {
		t.Fatalf("page url must keep the source episode number: %+v", chapters)
	}
}

func TestYeguoDetailRenumbersDuplicateNumbersContiguously(t *testing.T) {
	body := `{"status":"1","data":{"video_id":"3420","title":"50元买断人妻","episodes":[` +
		`{"id":"186775","sort":1,"title":"50元买断人妻 第4集简介"},` +
		`{"id":"186776","sort":1,"title":"50元买断人妻 第1集"},` +
		`{"id":"186777","sort":1,"title":"50元买断人妻 第2集"},` +
		`{"id":"186778","sort":3,"title":"50元买断人妻 第3集"}]}}`
	downloader := yeguoDetailFixtureDownloader(t, body)
	_, chapters, err := downloader.fetchYeguoDetail(context.Background(), "3420")
	if err != nil {
		t.Fatalf("duplicate source numbers must not fail the source: %v", err)
	}
	if len(chapters) != 4 {
		t.Fatalf("chapters = %d, want 4: %+v", len(chapters), chapters)
	}
	seen := map[string]bool{}
	for index, chapter := range chapters {
		number := chapter.EpisodeString(0)
		if number != strconv.Itoa(index+1) {
			t.Fatalf("chapter %d number = %s, want %d（重编必须从 1 开始且连续）", index, number, index+1)
		}
		if seen[number] {
			t.Fatalf("chapter number %s duplicated: %+v", number, chapters)
		}
		seen[number] = true
	}
	if !strings.Contains(chapters[0].Title, "第1集") || !strings.Contains(chapters[3].Title, "第4集") {
		t.Fatalf("titles were not ordered by their own episode numbers: %+v", chapters)
	}
}

func TestYeguoDetailKeepsUnnumberedEpisodesInDocumentOrder(t *testing.T) {
	body := `{"status":"1","data":{"video_id":"2344","title":"高三爱情故事","episodes":[` +
		`{"id":"187092","sort":7,"title":"高三爱情故事序章1"},` +
		`{"id":"187091","sort":7,"title":"高三爱情故事序章2"},` +
		`{"id":"153359","sort":"","title":""},` +
		`{"id":"153360","title":"AI短剧  高三爱情故事 素素 2"}]}}`
	downloader := yeguoDetailFixtureDownloader(t, body)
	_, chapters, err := downloader.fetchYeguoDetail(context.Background(), "2344")
	if err != nil {
		t.Fatalf("unusable source numbers must not fail the source: %v", err)
	}
	if len(chapters) != 4 {
		t.Fatalf("chapters = %d, want 4: %+v", len(chapters), chapters)
	}
	for index, chapter := range chapters {
		if chapter.EpisodeString(0) != strconv.Itoa(index+1) {
			t.Fatalf("chapter %d number = %s, want %d", index, chapter.EpisodeString(0), index+1)
		}
		if !strings.HasPrefix(chapter.ID, "yeguo:2344:") || chapter.VideoURL == "" {
			t.Fatalf("chapter identity lost: %+v", chapter)
		}
	}
	if chapters[2].Title != "第 3 集" {
		t.Fatalf("empty source title should fall back to the normalized number: %+v", chapters[2])
	}
	if chapters[3].Title != "AI短剧  高三爱情故事 素素 2" {
		t.Fatalf("source title must be preserved: %+v", chapters[3])
	}
}

func TestYeguoDetailFailsOnlyWithoutPlayableEpisodes(t *testing.T) {
	body := `{"status":"1","data":{"video_id":"9001","title":"空目录","episodes":[` +
		`{"id":"0","sort":1,"title":"无效编号"},` +
		`{"id":"9001","sort":2,"title":"广告","is_adv":1}]}}`
	downloader := yeguoDetailFixtureDownloader(t, body)
	if _, _, err := downloader.fetchYeguoDetail(context.Background(), "9001"); err == nil {
		t.Fatal("a detail without any playable episode should still report an error")
	}
}
