package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const hongguoRegistrationFixture = `{"device_id":9123456789012345678,"install_id":9234567890123456789,"device_token":"must-not-be-saved"}`

func TestHongguoRegistrationPersistsAndSurvivesCatalogRestore(t *testing.T) {
	var registrations atomic.Int32
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "log.snssdk.com" || r.URL.Path != "/service/2/device_register/" || r.Method != http.MethodPost {
			t.Errorf("unexpected registration request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("source credentials leaked to registration")
		}
		registrations.Add(1)
		return sourceFixtureResponse(r, 200, hongguoRegistrationFixture), nil
	})
	var group sync.WaitGroup
	for range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			id, err := d.ensureHongguoRegistration(context.Background())
			if err != nil || id.DeviceID != "9123456789012345678" || id.InstallID != "9234567890123456789" {
				t.Error("registration lost precision or failed", err)
			}
		}()
	}
	group.Wait()
	if registrations.Load() != 1 {
		t.Fatal("concurrent calls registered different identities", registrations.Load())
	}
	body, err := os.ReadFile(filepath.Join(d.cfg.dataDir, "hongguo-device.json"))
	var stored map[string]any
	if err != nil || json.Unmarshal(body, &stored) != nil || len(stored) != 3 || stored["device_token"] != nil {
		t.Fatal("unexpected persisted identity data", err)
	}
	restored := sourceFixtureDownloader(t, func(*http.Request) (*http.Response, error) {
		t.Error("restart attempted another registration")
		return nil, errors.New("fixture offline")
	})
	restored.cfg.dataDir = d.cfg.dataDir
	feed := hongguoAppGenres[0].key
	restored.restoreHongguoCatalog(&hongguoCatalogState{Version: 1, DeviceID: "1001", InstallID: "1002", Feeds: map[string]hongguoCatalogCursor{feed: {Initialized: true, Offset: 20}}})
	id, err := restored.ensureHongguoRegistration(context.Background())
	client := restored.hongguoClient()
	if err != nil || client.state.DeviceID != id.DeviceID || client.state.InstallID != id.InstallID || client.state.Feeds[feed].Offset != 20 {
		t.Fatal("old catalog overwrote registered identity or feed state", err)
	}
}

func TestHongguoRegistrationSaveFailureRetainsIdentity(t *testing.T) {
	var registrations int
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		registrations++
		return sourceFixtureResponse(r, 200, hongguoRegistrationFixture), nil
	})
	d.hongguoClient()
	path := filepath.Join(d.cfg.dataDir, "hongguo-device.json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ensureHongguoRegistration(context.Background()); err == nil {
		t.Fatal("failed identity persistence was hidden")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	id, err := d.ensureHongguoRegistration(context.Background())
	if err != nil || registrations != 1 || id.DeviceID != "9123456789012345678" {
		t.Fatal("save retry registered a different identity", err, registrations)
	}
}

func TestHongguoRegistrationRejectsCorruptionWithoutOverwriting(t *testing.T) {
	d := sourceFixtureDownloader(t, func(*http.Request) (*http.Response, error) {
		t.Error("corrupt identity triggered replacement registration")
		return nil, errors.New("fixture offline")
	})
	path := filepath.Join(d.cfg.dataDir, "hongguo-device.json")
	if err := os.WriteFile(path, []byte("damaged fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ensureHongguoRegistration(context.Background()); err == nil {
		t.Fatal("corrupt identity was accepted")
	}
	body, _ := os.ReadFile(path)
	if string(body) != "damaged fixture" {
		t.Fatal("corrupt identity was overwritten")
	}
}

func TestHongguoRegistrationCancellationDoesNotQueueNetworkWork(t *testing.T) {
	d := sourceFixtureDownloader(t, func(*http.Request) (*http.Response, error) {
		t.Error("canceled waiter started registration")
		return nil, errors.New("fixture offline")
	})
	client := d.hongguoClient()
	client.registrationGate = make(chan struct{}, 1)
	client.registrationGate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := d.ensureHongguoRegistration(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("waiting registration did not honor cancellation", err)
	}
	<-client.registrationGate
	if _, err := d.ensureHongguoRegistration(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("already-canceled registration started work", err)
	}
}

func TestHongguoRegisteredPlaybackRetriesEmptyResponseWithoutRotating(t *testing.T) {
	const vid = "80001"
	registrations, posts := 0, 0
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "log.snssdk.com" {
			registrations++
			response := sourceFixtureResponse(r, 200, hongguoRegistrationFixture)
			response.Header.Set("Set-Cookie", "registration_only=secret; Path=/")
			return response, nil
		}
		posts++
		if r.URL.Query().Get("device_id") != "9123456789012345678" || r.URL.Query().Get("iid") != "9234567890123456789" || r.Header.Get("Cookie") != "" {
			t.Error("playback rotated identity or leaked registration cookie")
		}
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-SS-Stub") != hgMd5HexUpper(body) || r.Header.Get("X-Argus") == "" || r.Header.Get("X-Medusa") != "" || r.URL.Query().Get("version_code") != "73532" || r.Header.Get("User-Agent") != hongguoAppUserAgent {
			t.Error("playback request was not signed after preparation")
		}
		var payload hongguoMultiPayload
		if json.Unmarshal(body, &payload) != nil || len(payload.MixedVideoIDMap["1004"]) != 1 || payload.MixedVideoIDMap["1004"][0] != vid {
			t.Error("requested episode was replaced")
		}
		if posts == 1 {
			return sourceFixtureResponse(r, 200, ""), nil
		}
		return sourceFixtureResponse(r, 200, `{"code":0,"data":{"80001":{"video_model":{"video_id":"80001"}}}}`), nil
	})
	entry, err := d.hongguoPlayerPost(context.Background(), vid)
	if err != nil || len(entry) == 0 || registrations != 1 || posts != 2 {
		t.Fatal("bounded empty-response retry failed", err, registrations, posts)
	}
	if _, err := d.hongguoPlayerPost(context.Background(), vid); err != nil || registrations != 1 || posts != 3 {
		t.Fatal("later playback did not reuse identity", err)
	}
}

func TestHongguoRegisteredPlaybackStopsOnDenialAndRetryAfter(t *testing.T) {
	for _, status := range []int{200, 403, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			posts := 0
			d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "log.snssdk.com" {
					return sourceFixtureResponse(r, 200, hongguoRegistrationFixture), nil
				}
				posts++
				response := sourceFixtureResponse(r, status, "")
				response.Header.Set("Retry-After", "30")
				response.Header.Set("Server", "fixture")
				return response, nil
			})
			if _, err := d.hongguoPlayerPost(context.Background(), "80001"); err == nil || posts != 1 {
				t.Fatal("denial or requested cooldown was retried", err, posts)
			}
		})
	}
}

func TestHongguoRegisteredPlaybackRejectsWrongEpisode(t *testing.T) {
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "log.snssdk.com" {
			return sourceFixtureResponse(r, 200, hongguoRegistrationFixture), nil
		}
		return sourceFixtureResponse(r, 200, `{"code":0,"data":{"80002":{"video_model":{"video_id":"v03fixture"}}}}`), nil
	})
	if _, err := d.resolveHongguoAppMedia(context.Background(), "80001"); err == nil || err.Error() != "红果 App 接口未返回所选分集" {
		t.Fatal("mismatched video model was accepted", err)
	}
}

func TestHongguoRegisteredPlaybackAcceptsInternalMediaID(t *testing.T) {
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "log.snssdk.com" {
			return sourceFixtureResponse(r, 200, hongguoRegistrationFixture), nil
		}
		return sourceFixtureResponse(r, 200, `{"code":0,"data":{"80001":{"video_model":{"video_id":"v03fixture","video_list":[{"main_url":"https://media.example.test/selected.mp4","video_meta":{"codec_type":"h264","definition":"720p"}}]}}}}`), nil
	})
	media, err := d.resolveHongguoAppMedia(context.Background(), "80001")
	if err != nil || media.URL != "https://media.example.test/selected.mp4" {
		t.Fatal("internal media ID was confused with chapter ID", media.URL, err)
	}
}
