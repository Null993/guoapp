package core

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestHuangguoVideoUsesDomesticOriginAcrossDetailAndMedia(t *testing.T) {
	var paths []string
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "huangguoai.fit" {
			t.Errorf("unexpected origin %s", r.URL.Host)
		}
		if r.Header.Get("Cookie") != "" {
			t.Error("old-origin cookie leaked to domestic route")
		}
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/series/sample":
			return sourceFixtureResponse(r, 200, `<h1>合成作品</h1><div class="series-episodes"><a href="/video/one">第1集</a></div>`), nil
		case "/video/one":
			return sourceFixtureResponse(r, 200, `<div data-hls="/uploads/master.m3u8"></div>`), nil
		case "/uploads/master.m3u8":
			if r.Header.Get("Referer") != "https://huangguoai.fit/video/one" || r.Header.Get("Origin") != "https://huangguoai.fit" {
				t.Error("media headers retained obsolete origin", r.Header)
			}
			return sourceFixtureResponse(r, 200, "#EXTM3U\n#EXTINF:12,\nsegment.ts\n#EXT-X-ENDLIST\n"), nil
		}
		return nil, errors.New("unexpected fixture path")
	})
	d.cfg.HuangguoVideoURL = ""
	d.sourceAccess = map[string]nativeSourceAccess{sourceHuangguoVideo: {Origin: huangguoVideoBaseURL, Cookie: "old=secret"}}
	body, actual, err := d.fetchProviderTextPage(context.Background(), huangguoVideoBaseURL+"/series/sample", huangguoVideoBaseURL+"/")
	if err != nil || !strings.Contains(body, "合成作品") || actual != "https://huangguoai.fit/series/sample" {
		t.Fatal(actual, err)
	}
	media, err := d.resolveProviderMedia(context.Background(), Task{DramaID: "huangguo-video:series/sample", Chapter: Chapter{Source: sourceHuangguoVideo, PageURL: huangguoVideoBaseURL + "/video/one", CurrentEpisode: rawEpisode(1)}})
	if err != nil || media.URL != "https://huangguoai.fit/uploads/master.m3u8" || !strings.Contains(media.Playlist, "#EXTM3U") {
		t.Fatal(media, err)
	}
	if len(paths) != 3 {
		t.Fatal(paths)
	}
}

func TestHuangguoVideoDomesticRoutesRespectConfigurationAndDenials(t *testing.T) {
	d := sourceFixtureDownloader(t, nil)
	d.cfg.HuangguoVideoURL = ""
	routes := d.providerURLCandidates(huangguoVideoBaseURL + "/videos?page=2")
	if len(routes) != 2 || routes[0] != "https://huangguoai.fit/videos?page=2" || routes[1] != huangguoVideoBaseURL+"/videos?page=2" {
		t.Fatal(routes)
	}
	d.cfg.HuangguoVideoURL = "https://custom.example.test"
	routes = d.providerURLCandidates(huangguoVideoBaseURL + "/videos?page=2")
	if len(routes) != 1 || routes[0] != "https://custom.example.test/videos?page=2" {
		t.Fatal(routes)
	}
	for _, status := range []int{403, 429} {
		hits := 0
		d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
			hits++
			response := sourceFixtureResponse(r, status, "denied")
			response.Header.Set("Retry-After", "30")
			return response, nil
		})
		d.cfg.HuangguoVideoURL = ""
		if _, err := d.fetchProviderText(context.Background(), huangguoVideoBaseURL+"/videos", huangguoVideoBaseURL+"/"); err == nil || hits != 1 {
			t.Fatal("denied route rotated to another domain", status, hits, err)
		}
	}
}

func TestHuangguoVideoMirrorPreservesIdentityAndHeaderBoundaries(t *testing.T) {
	d := sourceFixtureDownloader(t, nil)
	d.cfg.HuangguoVideoURL = ""
	if providerSourceForURL("https://huangguoai.fit/videos") != sourceHuangguoVideo || canonicalProviderSource("huangguoai.fit") != sourceHuangguoVideo {
		t.Fatal("domestic video route became the AI source")
	}
	if providerSourceForURL("https://huangguoai.fit.evil.test/") != "" {
		t.Fatal("untrusted mirror accepted")
	}
	request, _ := http.NewRequest(http.MethodGet, "https://huangguoai.fit/video/one", nil)
	request.Header.Set("Referer", huangguoVideoBaseURL+"/video/one")
	if huangguoBrowserHeaders(request)["sec-fetch-site"][0] != "cross-site" {
		t.Fatal("different registrable domains marked same-site")
	}
	if got := d.huangguoVideoAssetURL(huangguoVideoBaseURL + "/uploads/cover.webp?v=4"); got != "https://huangguoai.fit/uploads/cover.webp?v=4" {
		t.Fatal(got)
	}
	if got := d.huangguoVideoAssetURL("https://cdn.example.test/cover.webp"); got != "https://cdn.example.test/cover.webp" {
		t.Fatal(got)
	}
}
