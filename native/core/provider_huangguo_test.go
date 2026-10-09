package core

import (
	"strconv"
	"strings"
	"testing"
)

func TestHuangguoAIEpisodeKeyRequiresOwnSeries(t *testing.T) {
	cases := []struct {
		href string
		want string
	}{
		{"/video/own-1/1", "1"},
		{"/video/own-1/ep-4", "ep-4"},
		{"https://huangguoai.com/video/own-1/12", "12"},
		{"/video/other-1/2", ""},
		{"/detail/own-1/", ""},
		{"/video/own-1", ""},
		{"/video/own-1/", ""},
		{"/search?q=own-1", ""},
		{"", ""},
	}
	for _, testCase := range cases {
		if got := aiEpisodeKey(testCase.href, "own-1"); got != testCase.want {
			t.Fatalf("aiEpisodeKey(%q) = %q, want %q", testCase.href, got, testCase.want)
		}
	}
}

func TestHuangguoAIEpisodesIgnoreForeignVideoLinks(t *testing.T) {
	pageURL := "https://huangguoai.com/detail/own-1/"
	rawHTML := `<main>
	<section class="episode-list">
		<a href="/video/foreign-1/1" title="第1集">第1集</a>
		<a href="/video/foreign-2/1" title="第1集">第1集</a>
		<a href="/video/own-1/1" title="第1集">第1集</a>
		<a href="/video/own-1/2" title="第2集">第2集</a>
		<a href="/video/own-1/3" title="第3集">第3集</a>
	</section>
	<section class="related">
		<h3>猜你喜欢</h3>
		<a href="/video/foreign-2/1" title="第1集">第1集</a>
		<a href="/video/foreign-3/1" title="第1集">第1集</a>
	</section>
	</main>`
	episodes := parseHuangguoAIEpisodes(rawHTML, pageURL, "own-1", 30)
	if len(episodes) != 3 {
		t.Fatalf("episodes = %d, want 3: %+v", len(episodes), episodes)
	}
	for i, episode := range episodes {
		if episode.Key != strconv.Itoa(i+1) || episode.Index != i+1 {
			t.Fatalf("episode %d = %+v, want key/index %d", i, episode, i+1)
		}
		if !strings.Contains(episode.URL, "/video/own-1/") {
			t.Fatalf("foreign episode leaked: %+v", episode)
		}
	}
}

func TestHuangguoAIEpisodesDeduplicateRepeatedLinks(t *testing.T) {
	rawHTML := `<a href="/video/own-3/1" title="第1集">第1集</a>` +
		`<a href="/video/own-3/1?ref=1" title="第1集">第1集</a>` +
		`<a href="/video/own-3/2" title="第2集">第2集</a>`
	episodes := parseHuangguoAIEpisodes(rawHTML, "https://huangguoai.com/detail/own-3/", "own-3", 0)
	if len(episodes) != 2 {
		t.Fatalf("episodes = %d, want 2: %+v", len(episodes), episodes)
	}
	if episodes[0].Index != 1 || episodes[1].Index != 2 {
		t.Fatalf("episode indexes are not deduplicated ascending: %+v", episodes)
	}
}

func TestHuangguoAIEpisodesRespectDeclaredTotal(t *testing.T) {
	var body strings.Builder
	body.WriteString(`<section class="episode-list">`)
	for i := 1; i <= 6; i++ {
		n := strconv.Itoa(i)
		body.WriteString(`<a href="/video/own-2/` + n + `" title="第` + n + `集">第` + n + `集</a>`)
	}
	body.WriteString(`</section>`)
	episodes := parseHuangguoAIEpisodes(body.String(), "https://huangguoai.com/detail/own-2/", "own-2", 3)
	if len(episodes) != 3 {
		t.Fatalf("episodes = %d, want 3: %+v", len(episodes), episodes)
	}
	for i, episode := range episodes {
		if episode.Index != i+1 {
			t.Fatalf("episode %d index = %d, want %d", i, episode.Index, i+1)
		}
	}
}

func TestHuangguoVideoSectionHandlesNestedHeadingAndRecommendations(t *testing.T) {
	rawHTML := `<html><body>
	<h2><span>选集</span></h2>
	<article class="video-card"><a href="/video/ep-1" title="第1集">第1集</a></article>
	<article class="video-card"><a href="/video/ep-2" title="第2集">第2集</a></article>
	<article class="video-card"><a href="/video/ep-3" title="第3集">第3集</a></article>
	<h2>相关推荐</h2>
	<article class="video-card"><a href="/video/hot-9" title="第1集">第1集</a></article>
	</body></html>`
	section := huangguoVideoEpisodeSection(rawHTML)
	if strings.Contains(section, "hot-9") {
		t.Fatalf("recommendation cards leaked into the episode section: %s", section)
	}
	episodes := parseHuangguoVideoEpisodesScoped(rawHTML, "https://huangguo.video/series/own-9", "own-9", 6)
	if len(episodes) != 3 {
		t.Fatalf("episodes = %d, want 3: %+v", len(episodes), episodes)
	}
	for i, episode := range episodes {
		if episode.Index != i+1 || episode.Key != "video/ep-"+strconv.Itoa(i+1) {
			t.Fatalf("episode %d = %+v", i, episode)
		}
	}
}

func TestHuangguoVideoEpisodesScopeToSeriesIDWhenPresent(t *testing.T) {
	rawHTML := `<div>
	<h3>选集</h3>
	<article class="video-card"><a href="/video/own-9/1" title="第1集">第1集</a></article>
	<article class="video-card"><a href="/video/own-9/2" title="第2集">第2集</a></article>
	<article class="video-card"><a href="/video/foreign-9/1" title="第1集">第1集</a></article>
	</div>`
	episodes := parseHuangguoVideoEpisodesScoped(rawHTML, "https://huangguo.video/series/own-9", "own-9", 10)
	if len(episodes) != 2 {
		t.Fatalf("episodes = %d, want 2: %+v", len(episodes), episodes)
	}
	for _, episode := range episodes {
		if !strings.Contains(episode.Key, "own-9") {
			t.Fatalf("foreign episode kept: %+v", episode)
		}
	}
}

func TestHuangguoVideoEpisodesUseEpisodeContainerWithoutHeading(t *testing.T) {
	rawHTML := `<div class="page">
	<div class="episode-list"><a href="/video/ep-1" title="第1集">第1集</a><a href="/video/ep-2" title="第2集">第2集</a></div>
	</div>`
	episodes := parseHuangguoVideoEpisodesScoped(rawHTML, "https://huangguo.video/series/own-9", "own-9", 0)
	if len(episodes) != 2 {
		t.Fatalf("episodes = %d, want 2: %+v", len(episodes), episodes)
	}
}

func TestHuangguoVideoEpisodesStayEmptyWithoutEpisodeSection(t *testing.T) {
	rawHTML := `<div class="page">
	<article class="video-card"><a href="/video/foreign-1/1" title="第1集">第1集</a></article>
	<h2>相关推荐</h2>
	<article class="video-card"><a href="/video/foreign-2/1" title="第1集">第1集</a></article>
	</div>`
	if episodes := parseHuangguoVideoEpisodesScoped(rawHTML, "https://huangguo.video/series/own-9", "own-9", 0); len(episodes) != 0 {
		t.Fatalf("expected a conservative empty parse, got %+v", episodes)
	}
}

func TestHuangguoVideoEpisodesLegacyEntryPrefersNumberedLinks(t *testing.T) {
	rawHTML := `<a href="/video/two">开始观看</a><a href="/video/one"><p>第1集</p></a><a href="/video/two"><p>第2集</p></a>`
	episodes := parseHuangguoVideoEpisodes(rawHTML, "https://huangguo.video/series/synthetic")
	if len(episodes) != 2 || episodes[0].Key != "video/one" || episodes[1].Index != 2 {
		t.Fatalf("legacy video entry regressed: %+v", episodes)
	}
}
