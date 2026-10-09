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
		{"/video/own-1", "ep-1"},
		{"/video/own-1/", "ep-1"},
		{"/video/other-1/2", ""},
		{"/detail/own-1/", ""},
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

func TestHuangguoAIEpisodesKeepBareFirstEpisodeFromRealGrid(t *testing.T) {
	rawHTML := `<section class="hg-web-play" data-web-play data-show-episodes="1" data-hg-next-url="/video/3520/ep-2/">
	<a class="hg-web-play__action" data-web-play-ep-next href="/video/3520/ep-2/">下一集</a>
	<div class="hg-web-play__ep-grid" data-web-play-ep-grid>
	<a class="hg-web-play__ep is-active" href="/video/3520/" data-ep-id="1">01</a><a class="hg-web-play__ep" href="/video/3520/ep-2/" data-ep-id="2">02</a><a class="hg-web-play__ep" href="/video/3520/ep-3/" data-ep-id="3">03</a><a class="hg-web-play__ep" href="/video/3520/ep-4/" data-ep-id="4">04</a><a class="hg-web-play__ep" href="/video/3520/ep-5/" data-ep-id="5">05</a><a class="hg-web-play__ep" href="/video/3520/ep-6/" data-ep-id="6">06</a>
	</div>
	<aside class="hg-web-play__recommend"><a href="/video/12/" data-ep-id="1">相关推荐</a></aside>
	</section>`
	episodes := parseHuangguoAIEpisodes(rawHTML, "https://huangguoai.com/detail/3520/", "3520", 6)
	if len(episodes) != 6 {
		t.Fatalf("episodes = %d, want 6: %+v", len(episodes), episodes)
	}
	wantKeys := []string{"ep-1", "ep-2", "ep-3", "ep-4", "ep-5", "ep-6"}
	for i, episode := range episodes {
		if episode.Index != i+1 || episode.Key != wantKeys[i] {
			t.Fatalf("episode %d = %+v, want key %q index %d", i, episode, wantKeys[i], i+1)
		}
	}
	if !strings.HasSuffix(episodes[0].URL, "/video/3520/") {
		t.Fatalf("first episode URL lost its bare link: %+v", episodes[0])
	}
}

func TestHuangguoAIEpisodesBareFirstEpisodeKeepsContiguousNumbering(t *testing.T) {
	rawHTML := `<a class="hg-web-play__action" data-web-play-ep-next href="/video/own-7/ep-2/">下一集</a>` +
		`<a href="/video/own-7/" title="第1集">第1集</a>` +
		`<a href="/video/own-7/ep-2/" title="第2集">第2集</a>` +
		`<a href="/video/own-7/ep-3/" title="第3集">第3集</a>`
	episodes := parseHuangguoAIEpisodes(rawHTML, "https://huangguoai.com/detail/own-7/", "own-7", 3)
	if len(episodes) != 3 {
		t.Fatalf("episodes = %d, want 3: %+v", len(episodes), episodes)
	}
	for i, episode := range episodes {
		if episode.Index != i+1 {
			t.Fatalf("episode %d index = %d, want %d: %+v", i, episode.Index, i+1, episodes)
		}
	}
	if episodes[0].Key != "ep-1" || episodes[2].Key != "ep-3" {
		t.Fatalf("episode keys changed: %+v", episodes)
	}
}

func TestHuangguoAIEpisodesRenumberWhenSourceNumberingStartsAtTwo(t *testing.T) {
	rawHTML := `<a href="/video/own-9/ep-2/" title="第2集">第2集</a>` +
		`<a href="/video/own-9/ep-3/" title="第3集">第3集</a>` +
		`<a href="/video/own-9/ep-4/" title="第4集">第4集</a>`
	episodes := parseHuangguoAIEpisodes(rawHTML, "https://huangguoai.com/detail/own-9/", "own-9", 0)
	if len(episodes) != 3 {
		t.Fatalf("episodes = %d, want 3: %+v", len(episodes), episodes)
	}
	for i, episode := range episodes {
		if episode.Index != i+1 {
			t.Fatalf("episode %d index = %d, want %d: %+v", i, episode.Index, i+1, episodes)
		}
	}
	if episodes[0].Key != "ep-2" || episodes[2].Key != "ep-4" {
		t.Fatalf("source keys must be preserved: %+v", episodes)
	}
}

func TestHuangguoAIEpisodesTruncateAfterSorting(t *testing.T) {
	var body strings.Builder
	for i := 6; i >= 1; i-- {
		n := strconv.Itoa(i)
		body.WriteString(`<a href="/video/own-8/ep-` + n + `" title="第` + n + `集">第` + n + `集</a>`)
	}
	episodes := parseHuangguoAIEpisodes(body.String(), "https://huangguoai.com/detail/own-8/", "own-8", 3)
	if len(episodes) != 3 {
		t.Fatalf("episodes = %d, want 3: %+v", len(episodes), episodes)
	}
	for i, episode := range episodes {
		wantKey := "ep-" + strconv.Itoa(i+1)
		if episode.Index != i+1 || episode.Key != wantKey {
			t.Fatalf("episode %d = %+v, want key %q index %d", i, episode, wantKey, i+1)
		}
	}
}
