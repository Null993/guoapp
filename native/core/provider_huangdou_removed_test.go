package core

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestHuangdouPlaybackAddsOnlyFullEpisodeLines(t *testing.T) {
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/drama/play" {
			t.Fatalf("unexpected playback endpoint: %s", request.URL.Path)
		}
		return sourceFixtureResponse(request, 200, `{"status":"y","data":{
			"m3u8":"/main.m3u8","preview_m3u8":"/teaser.m3u8","hls_key":"0123456789abcdef0123456789abcdef","duration":20,
			"lines":[{"url":"/main.m3u8"},{"url":"/teaser.m3u8"},{"url":"/short.m3u8","is_preview":true},
			{"url":"ftp://bad.test/media.m3u8"},{"m3u8":"/backup.m3u8"},{"play_url":"/backup.m3u8"}]}}`), nil
	})
	client := newHuangdouAPIClient(d)
	media, err := d.resolveHuangdouPlayback(context.Background(), client, "100", 2)
	if err != nil || media.URL != huangdouBaseURL+"/main.m3u8" || len(media.Variants) != 1 {
		t.Fatalf("unexpected result: %+v, %v", media, err)
	}
	backup := media.Variants[0]
	if backup.URL != huangdouBaseURL+"/backup.m3u8" || backup.Referer != media.Referer || backup.Duration != media.Duration || !bytes.Equal(backup.HLSKey, media.HLSKey) {
		t.Fatalf("backup lost episode media settings: %+v", backup)
	}
}

func TestHuangdouMetadataRejectNeedsCatalogConfirmation(t *testing.T) {
	for _, fixture := range []struct {
		name, code, list string
		status           int
		missing          bool
		probes           int
	}{
		{"removed", "2001", `[]`, 200, true, 1},
		{"still-listed", "2001", `[{"id":"123","name":"合成旧条目"}]`, 200, false, 1},
		{"replacement-id", "2001", `[{"id":"456","name":"合成旧条目"}]`, 200, true, 1},
		{"explicit-reference", "2001", `[{"id":"456","drama_id":"rp_123","name":"合成旧条目"}]`, 200, false, 1},
		{"authorization", "813005", `[]`, 200, false, 0},
		{"malformed-list", "2001", `{}`, 200, false, 1},
		{"network-failure", "2001", `[]`, 503, false, 1},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			probes := 0
			engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == "/api/drama/detail" {
					return sourceFixtureResponse(request, 200, fmt.Sprintf(`{"status":"n","errorCode":%q,"msg":"请求数据错误"}`, fixture.code)), nil
				}
				if request.URL.Path != "/api/drama/list" {
					t.Fatal(request.URL.Path)
				}
				probes++
				return sourceFixtureResponse(request, fixture.status, fmt.Sprintf(`{"status":"y","data":{"list":%s}}`, fixture.list)), nil
			})
			item := engine.fetchSourceMetadata(context.Background(), nativeDrama{ID: "huangdou:123", Source: sourceHuangdou, Title: "合成旧条目"}, 0)
			if item.err == nil || sourceMetadataMissing(item.err) != fixture.missing || probes != fixture.probes {
				t.Fatal(item.err, probes)
			}
			if fixture.name == "still-listed" || fixture.name == "explicit-reference" {
				if _, temporary := engine.sourceRetryDelay(item.err); !temporary {
					t.Fatal("listed metadata rejection did not receive automatic retry", item.err)
				}
			}
		})
	}
}
