package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRouteLatencyCacheHasAHardLimit(t *testing.T) {
	engine := &nativeEngine{}
	for i := 0; i < routeLatencyMaxEntry+20; i++ {
		engine.recordRouteLatency(fmt.Sprint(i), time.Millisecond)
	}
	if len(engine.routeLatencies) != routeLatencyMaxEntry {
		t.Fatalf("cache grew to %d", len(engine.routeLatencies))
	}
}

func TestRouteProbeCancellationAndSuccessfulStatus(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Range") != "bytes=0-1023" {
			t.Error("probe read beyond its range")
		}
		return &http.Response{StatusCode: http.StatusRequestedRangeNotSatisfiable, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
	})
	media := providerMedia{URL: "https://synthetic.test/video"}
	if rtt := engine.measureRoute(context.Background(), media, time.Second); rtt != 0 {
		t.Fatal("failed route was cached as healthy")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	engine.probeRoutes(ctx, nativePlaybackChoice{media: []providerMedia{media, {URL: "https://synthetic.test/backup"}}}, time.Second)
	if len(engine.routeLatencies) != 0 {
		t.Fatal("cancelled probe changed cache")
	}
}
