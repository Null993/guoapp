package core

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

const huangguoCoverRefreshPage = `<script type="application/ld+json">{"@graph":[{"@type":"WebPage","url":"https://huangguoai.com/video/100/","name":"合成剧目"},{"@type":"VideoObject","url":"https://huangguoai.com/video/100/","name":"合成剧目","thumbnailUrl":["https://huangguoai.com/static/web/images/home_og.jpg"]}]}</script>
<section class="related"><img data-src="https://cover.example.test/wrong.jpg"></section>
<div class="hg-web-play__poster"><img src="data:image/gif;base64,R0lG" data-src="https://cover.example.test/own.png?auth_key=fresh"></div>`

func TestHuangguoAICoverRefreshesExpiredAddressAfterDetailRedirect(t *testing.T) {
	payload := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("synthetic")...)
	var oldHits, newHits, detailHits int
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Hostname() {
		case "cover.example.test":
			if request.URL.Query().Get("auth_key") == "expired" {
				oldHits++
				return sourceFixtureResponse(request, 403, "expired signature"), nil
			}
			newHits++
			if request.URL.String() != "https://cover.example.test/own.png?auth_key=fresh" {
				t.Error("loaded unrelated image", request.URL)
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(payload)), ContentLength: int64(len(payload)), Request: request}, nil
		case "huangguoai.com":
			detailHits++
			if request.URL.Path != "/detail/100/" || request.Header.Get("Cache-Control") != "no-cache" {
				t.Error("refresh did not request fresh own detail", request.URL)
			}
			return sourceFixtureResponse(request, 200, huangguoCoverRefreshPage), nil
		}
		t.Fatalf("unexpected request: %s", request.URL)
		return nil, nil
	})
	engine.covers = newNativeCoverCache(engine.directory, engine.downloader)
	drama := nativeDrama{ID: "huangguoai:100", Source: sourceHuangguoAI, Cover: "https://cover.example.test/own.png?auth_key=expired"}
	result, err := engine.loadCover(context.Background(), drama, false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(result["path"].(string))
	if err != nil || !bytes.Equal(data, payload) || oldHits != 1 || newHits != 1 || detailHits != 1 {
		t.Fatalf("cover refresh failed: %v old=%d new=%d detail=%d", err, oldHits, newHits, detailHits)
	}
	_, err = engine.loadCover(context.Background(), drama, false)
	if err != nil || oldHits != 1 || newHits != 1 || detailHits != 1 {
		t.Fatal("repaired cover was not reused", err)
	}
}

func TestHuangguoAICoverRejectsForeignDetailAndGenericPlaceholder(t *testing.T) {
	patch := Drama{ID: "huangguoai:100", Source: sourceHuangguoAI, SourceID: "100"}
	for _, path := range []string{"/video/101/", "/video/100/2/", "/series/100/"} {
		_, err := parseHuangguoSortDetail(strings.ReplaceAll(huangguoCoverRefreshPage, "/video/100/", path), "https://huangguoai.com/detail/100/", patch)
		if err == nil {
			t.Error("foreign detail accepted", path)
		}
	}
	body := strings.Split(huangguoCoverRefreshPage, "<section")[0]
	fresh, err := parseHuangguoSortDetail(body, "https://huangguoai.com/detail/100/", patch)
	if err != nil || nativeNormalize(fresh).Cover != "" {
		t.Fatal("generic site logo accepted as drama cover", err)
	}
}

func TestHuangguoVideoCoverSupportsCanonicalPageWithoutJSONLD(t *testing.T) {
	body := `<link rel="canonical" href="https://huangguo.video/series/own"><meta property="og:image" content="/uploads/own.webp?v=4"><h1>合成剧目</h1>`
	patch := Drama{ID: "huangguo-video:series/own", Source: sourceHuangguoVideo, SourceID: "series/own"}
	fresh, err := parseHuangguoSortDetail(body, "https://huangguo.video/series/own", patch)
	if err != nil || nativeNormalize(fresh).Cover != "https://huangguo.video/uploads/own.webp?v=4" || fresh.Title != "合成剧目" {
		t.Fatal("canonical page cover was not parsed", err)
	}
	for _, invalid := range []string{
		strings.ReplaceAll(body, "/series/own", "/series/other"),
		strings.ReplaceAll(body, "https://huangguo.video/series/own", "https://other.example/series/own"),
		strings.ReplaceAll(body, `<h1>合成剧目</h1>`, ""),
		strings.ReplaceAll(body, `rel="canonical"`, `rel="alternate"`),
	} {
		if _, err := parseHuangguoSortDetail(invalid, "https://huangguo.video/series/own", patch); err == nil {
			t.Error("unverified page accepted")
		}
	}
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/series/own" {
			return sourceFixtureResponse(request, 200, body), nil
		}
		if request.URL.String() != "https://huangguo.video/uploads/own.webp?v=4" {
			t.Error("wrong poster requested", request.URL)
		}
		return sourceFixtureResponse(request, 200, "GIF89a"), nil
	})
	engine.covers = newNativeCoverCache(engine.directory, engine.downloader)
	if _, err := engine.loadCover(context.Background(), nativeDrama{ID: patch.ID, Source: patch.Source}, false); err != nil {
		t.Fatal("empty-cache cover refresh failed", err)
	}
}
