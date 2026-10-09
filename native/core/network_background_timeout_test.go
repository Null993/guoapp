package core

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestBackgroundCatalogTimeoutOnlyCapsCallerTimeout(t *testing.T) {
	cases := []struct {
		timeout time.Duration
		want    time.Duration
	}{
		{0, nativeBackgroundRequestTimeout},
		{time.Second, time.Second},
		{8 * time.Second, 8 * time.Second},
		{15 * time.Second, 15 * time.Second},
		{nativeBackgroundRequestTimeout, nativeBackgroundRequestTimeout},
		{time.Minute, nativeBackgroundRequestTimeout},
	}
	for _, item := range cases {
		if got := backgroundRequestTimeout(item.timeout); got != item.want {
			t.Fatalf("background timeout for %s = %s, want %s", item.timeout, got, item.want)
		}
	}
}

func TestBackgroundCatalogRequestKeepsCallerDeadline(t *testing.T) {
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		deadline, ok := request.Context().Deadline()
		if !ok {
			return nil, fmt.Errorf("background catalog request lost its deadline")
		}
		if remaining := time.Until(deadline); remaining < 14*time.Second {
			return nil, fmt.Errorf("background catalog request deadline was shortened to %s", remaining)
		}
		return sourceFixtureResponse(request, http.StatusOK, `{"ok":true}`), nil
	})
	ctx := context.WithValue(context.Background(), backgroundCatalogKey{}, true)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.sorani.cc/sorani-cms/api/home/contentOptions", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := d.doCatalogRequestWithTimeout(request, 15*time.Second)
	if err != nil {
		t.Fatalf("background catalog request was cut short: %v", err)
	}
	response.Body.Close()
}

func TestBackgroundCatalogRequestWaitsOutSlowSite(t *testing.T) {
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		timer := time.NewTimer(9 * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
			return sourceFixtureResponse(request, http.StatusOK, `{"ok":true}`), nil
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	})
	ctx := context.WithValue(context.Background(), backgroundCatalogKey{}, true)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://analyze.buxefaex.cc/api/home/contentOptions", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := d.doCatalogRequestWithTimeout(request, 15*time.Second)
	if err != nil {
		t.Fatalf("background catalog request failed while the site was still responding: %v", err)
	}
	response.Body.Close()
}
