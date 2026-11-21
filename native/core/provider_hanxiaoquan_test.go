package core

import (
	"context"
	"net/http"
	"testing"
)

func TestHanxiaoquanTriesOnlyTheSameEpisodeAfterMissingPlaylist(t *testing.T) {
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/play/100-0-2.html":
			return sourceFixtureResponse(request, 200, `var now="https://cdn.example.test/missing.m3u8"`), nil
		case "/missing.m3u8":
			return sourceFixtureResponse(request, 404, "removed"), nil
		case "/hanxiaoquan/100.html":
			return sourceFixtureResponse(request, 200, `<a title="第3集" href="/play/100-0-2.html"></a>`+
				`<a title="第3集" href="/play/101-1-2.html"></a>`+
				`<a title="第2集" href="/play/100-1-2.html"></a>`+
				`<a title="第3集" href="/play/100-1-1.html"></a>`+
				`<a title="第3集" href="/play/100-2-2.html"></a>`), nil
		case "/play/100-2-2.html":
			return sourceFixtureResponse(request, 200, `var now="https://cdn.example.test/full.mp4"`), nil
		default:
			t.Fatalf("unexpected playback route: %s", request.URL.Path)
			return nil, nil
		}
	})
	page := hanxiaoquanSiteBaseURL + "/play/100-0-2.html"
	media, err := d.resolveHanxiaoquanMedia(context.Background(), Task{
		DramaID: providerDramaID(sourceHanxiaoquan, "100"),
		Chapter: Chapter{ID: providerChapterID(sourceHanxiaoquan, "100", page)},
	})
	if err != nil || media.URL != "https://cdn.example.test/full.mp4" {
		t.Fatalf("unexpected result: %+v %v", media, err)
	}
}
