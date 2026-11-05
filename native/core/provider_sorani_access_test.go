package core

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestSoraniImportedSessionIsScopedAndCleared(t *testing.T) {
	previous := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = previous }()
	authorized := true
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if authorized {
			if request.Header.Get("Authorization") != "Bearer synthetic-token" || request.Header.Get("Cookie") != "session=synthetic" || request.Header.Get("X-Sorani-Guest-Key") != "synthetic-guest" {
				t.Error("imported session missing from API request")
			}
		} else if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" || request.Header.Get("X-Sorani-Guest-Key") != "" {
			t.Error("cleared session survived in request headers")
		}
		return sourceFixtureResponse(request, 200, `{"code":200,"data":{}}`), nil
	})
	status := d.sourceAccessStatus(sourceSorani)
	if status["origin"] != "https://api.sorani.cc" || status["url"] != soraniSiteBaseURL {
		t.Fatal(status)
	}
	if _, err := d.saveSourceAccess(nativeInput{Source: sourceSorani, AccessToken: "Bearer synthetic-token", Cookie: "session=synthetic", DeviceID: "synthetic-guest"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.soraniRequest(context.Background(), "/video/episode/123/play", nil, 64<<10); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"https://www.sorani.net/", "https://cdn.sorani.cc/", "https://api.sorani.cc:8443/"} {
		request, _ := http.NewRequest(http.MethodGet, address, nil)
		if _, found := d.accessForRequest(request); found {
			t.Error("API credentials crossed origin boundary", address)
		}
	}
	d.clearSourceAccess(sourceSorani)
	authorized = false
	if _, err := d.soraniRequest(context.Background(), "/video/episode/123/play", nil, 64<<10); err != nil {
		t.Fatal(err)
	}
}

func TestSoraniLoginRequiredIsActionable(t *testing.T) {
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, 200, `{"code":200,"data":{"canPlay":false,"message":"请先登录","playUrl":null}}`), nil
	})
	_, err := d.resolveSoraniMedia(context.Background(), Task{DramaID: "sorani:123", Chapter: Chapter{ID: providerChapterID(sourceSorani, "123", "456")}})
	var authorization *sourceAuthorizationError
	if !errors.As(err, &authorization) || !isPermanentSourceError(err) {
		t.Fatal("login denial was treated as a retryable missing media URL", err)
	}
}
