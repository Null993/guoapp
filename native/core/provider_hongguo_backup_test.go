package core

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHongguoOfficialBackupKeepsRegisteredIdentityAndSelectedEpisode(t *testing.T) {
	primary, alternate, registrations := 0, 0, 0
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "log.snssdk.com":
			registrations++
			return sourceFixtureResponse(r, 200, hongguoRegistrationFixture), nil
		case "api5-normal-sinfonlineb.fqnovel.com":
			primary++
			return nil, errors.New("synthetic primary unavailable")
		case "api3-normal-sinfonlineb.fqnovel.com":
			alternate++
			if r.URL.Query().Get("device_id") != "9123456789012345678" || r.URL.Query().Get("iid") != "9234567890123456789" || r.Header.Get("X-Argus") == "" {
				t.Error("backup changed identity or omitted signature")
			}
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"1004":["80001"]`) {
				t.Error("backup replaced selected episode")
			}
			return sourceFixtureResponse(r, 200, `{"code":0,"data":{"80001":{"video_model":{"video_id":"v03fixture","fallback_api":"https://api5-normal-sinfonlineb.fqnovel.com/unavailable","video_list":[{"main_url":"https://cdn.example.test/hevc.mp4","video_meta":{"codec_type":"hevc","definition":"1080p"}},{"main_url":"https://cdn.example.test/h264.mp4","backup_url":"https://cdn.example.test/h264-backup.mp4","video_meta":{"codec_type":"h264","definition":"1080p"}}]}}}}`), nil
		}
		t.Error("backup unexpectedly depended on another route", r.URL.Host)
		return nil, errors.New("unexpected route")
	})
	media, err := d.resolveHongguoMedia(context.Background(), Task{DramaID: "hongguo:70001", Chapter: Chapter{VideoURL: "hongguo-cenc://80001"}})
	if err != nil || media.URL != "https://cdn.example.test/h264.mp4" || len(media.Variants) != 3 || primary != 1 || alternate != 1 || registrations != 1 {
		t.Fatal(media, err, primary, alternate, registrations)
	}
}

func TestHongguoOfficialBackupDoesNotRotateOnDenial(t *testing.T) {
	for _, status := range []int{403, 429} {
		primary, other := 0, 0
		d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "log.snssdk.com" {
				return sourceFixtureResponse(r, 200, hongguoRegistrationFixture), nil
			}
			if r.URL.Host == "api5-normal-sinfonlineb.fqnovel.com" {
				primary++
				response := sourceFixtureResponse(r, status, "denied")
				response.Header.Set("Retry-After", "30")
				return response, nil
			}
			other++
			return nil, errors.New("denied request rotated")
		})
		_, err := d.resolveHongguoMedia(context.Background(), Task{DramaID: "hongguo:70001", Chapter: Chapter{VideoURL: "hongguo-cenc://80001"}})
		if err == nil || primary != 1 || other != 0 {
			t.Fatal(status, err, primary, other)
		}
	}
}

func TestHongguoThirdPartyTLSFailureKeepsErrorAndAvoidsRepeatedConnections(t *testing.T) {
	hits := 0
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		hits++
		if r.URL.Scheme != "https" {
			t.Error("backup silently downgraded HTTPS")
		}
		return nil, errors.New("remote error: tls: no application protocol")
	})
	for range 2 {
		if _, err := d.resolveHongguoPlaybackAPI(context.Background(), "70001", "80001"); err == nil || !strings.Contains(err.Error(), "no application protocol") {
			t.Fatal(err)
		}
	}
	if hits != 1 {
		t.Fatal("broken third-party service repeatedly contacted", hits)
	}
}
