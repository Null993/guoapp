package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestHongguoPlaybackRegistrationPacket(t *testing.T) {
	packet, err := hongguoPlaybackRegistrationBody(time.Unix(1800000000, 0))
	if err != nil || len(packet) < 118 || !bytes.Equal(packet[:6], []byte{116, 99, 5, 16, 0, 0}) {
		t.Fatal("invalid registration envelope", err)
	}
	inner := sha512.Sum512(packet[6:38])
	derived := sha512.Sum512(append(inner[:], hongguoURLConstants...))
	block, err := aes.NewCipher(derived[:16])
	if err != nil {
		t.Fatal(err)
	}
	plain := append([]byte(nil), packet[38:]...)
	cipher.NewCBCDecrypter(block, derived[16:32]).CryptBlocks(plain, plain)
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > aes.BlockSize || !bytes.Equal(plain[len(plain)-padding:], bytes.Repeat([]byte{byte(padding)}, padding)) {
		t.Fatal("invalid packet padding")
	}
	plain = plain[:len(plain)-padding]
	digest := sha512.Sum512(plain[64:])
	if !bytes.Equal(plain[:64], digest[:]) {
		t.Fatal("registration checksum differs from compressed body")
	}
	reader, err := gzip.NewReader(bytes.NewReader(plain[64:]))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var container map[string]any
	if err := json.NewDecoder(reader).Decode(&container); err != nil {
		t.Fatal(err)
	}
	header := nestedMap(container, "header")
	if mapString(container, "magic_tag") != "ss_app_log" || mapString(header, "version_code") != "73932" || mapString(header, "cdid") == "" || mapString(header, "openudid") == "" {
		t.Fatal("incomplete registration profile")
	}
}

func TestHongguoCurrentPlaybackPreservesSelectedEpisodeAndSignature(t *testing.T) {
	registrations, plays := 0, 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "log5-applog-lf.fqnovel.com" {
			registrations++
			if request.Header.Get("Content-Type") != "application/octet-stream;tt-data=a" {
				t.Error("registration did not use encrypted protocol")
			}
			return sourceFixtureResponse(request, 200, hongguoRegistrationFixture), nil
		}
		plays++
		for _, header := range []string{"X-Gorgon", "X-Khronos", "X-Ladon", "X-Argus", "X-Helios", "X-Medusa"} {
			if request.Header.Get(header) == "" {
				t.Error("missing signature", header)
			}
		}
		if request.URL.Query().Get("device_id") != "9123456789012345678" || request.URL.Query().Get("iid") != "9234567890123456789" || request.URL.Query().Get("_rticket") != request.Header.Get("X-SS-Req-Ticket") {
			t.Error("request lost registered identity or matching timestamps")
		}
		body, _ := io.ReadAll(request.Body)
		var payload map[string]any
		json.Unmarshal(body, &payload)
		ids, _ := nestedMap(payload, "mixed_video_id_map")["1004"].([]any)
		if len(ids) != 1 || ids[0] != "80001" || mapString(nestedMap(payload, "biz_param"), "video_platform") != "1024" {
			t.Error("request replaced selected episode or changed protocol payload")
		}
		return sourceFixtureResponse(request, 200, `{"code":0,"data":{"80001":{"video_model":{"video_list":[]}}}}`), nil
	})
	for range 2 {
		entry, err := d.hongguoCurrentPlayerPostAt(context.Background(), "80001", hongguoAlternateAppBaseURL)
		if err != nil || entry["video_model"] == nil {
			t.Fatal(entry, err)
		}
	}
	if registrations != 2 || plays != 2 {
		t.Fatal("one-use registration was reused", registrations, plays)
	}
}

func TestHongguoCurrentPlaybackStopsOnRegistrationDenial(t *testing.T) {
	for _, status := range []int{403, 429} {
		requests := 0
		d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
			requests++
			return sourceFixtureResponse(request, status, "denied"), nil
		})
		_, err := d.hongguoCurrentPlayerPostAt(context.Background(), "80001", "")
		if err == nil || requests != 1 {
			t.Fatal(status, requests, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		t.Error("canceled registration reached transport")
		return nil, context.Canceled
	})
	if _, err := d.hongguoCurrentPlayerPostAt(ctx, "80001", ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestHongguoCurrentPlaybackUsesV5OnlyAfterEmptyReply(t *testing.T) {
	for _, status := range []int{200, 403, 429} {
		requests := 0
		d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
			requests++
			switch request.URL.Host {
			case "log5-applog-lf.fqnovel.com":
				return sourceFixtureResponse(request, 200, hongguoRegistrationFixture), nil
			case "api5-normal-sinfonlineb.fqnovel.com":
				return sourceFixtureResponse(request, status, ""), nil
			case "api5-normal-sinfonlinea.fqnovel.com":
				if request.Header.Get("Content-Encoding") != "gzip" || request.Header.Get("X-Helios") == "" || request.Header.Get("X-Medusa") == "" {
					t.Fatal("incomplete V5 request")
				}
				body, err := gzip.NewReader(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				defer body.Close()
				var payload map[string]any
				if err := json.NewDecoder(body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				ids := anyList(nestedMap(payload, "mixed_video_id_map")["1004"])
				if len(ids) != 1 || ids[0] != "80001" {
					t.Fatalf("unexpected episode: %v", ids)
				}
				return sourceFixtureResponse(request, 200, `{"code":0,"data":{"80001":{"video_model":{"video_list":[]}}}}`), nil
			default:
				t.Fatalf("unexpected host: %s", request.URL.Host)
				return nil, nil
			}
		})
		entry, err := d.hongguoCurrentPlayerPostAt(context.Background(), "80001", hongguoAppBaseURL)
		if status == 200 && (err != nil || entry["video_model"] == nil || requests != 3) {
			t.Fatalf("V5 advance failed: entry=%v err=%v requests=%d", entry, err, requests)
		}
		if status != 200 && (err == nil || requests != 2) {
			t.Fatalf("unexpected request after denial: status=%d err=%v requests=%d", status, err, requests)
		}
	}
}

func TestHongguoV5PlaybackUsesTabForTheSameEpisodeAndIdentity(t *testing.T) {
	const videoID = "80001"
	identity := hongguoRegistration{Version: 1, DeviceID: "9123456789012345678", InstallID: "9234567890123456789"}
	for _, test := range []struct {
		name       string
		postStatus int
		postBody   string
		tabBody    string
		wantError  bool
	}{
		{"tab response", 200, "", `{"code":0,"data":{"video_model_data":{"80001":{"video_model":{}}}}}`, false},
		{"wrong episode", 200, "", `{"code":0,"data":{"video_model_data":{"80002":{"video_model":{}}}}}`, true},
		{"no permission", 200, "", `{"code":403,"data":{"80001":{"video_model":{}}}}`, true},
		{"post forbidden", 403, "", "", true},
		{"post throttled", 429, "", "", true},
		{"post success", 200, `{"code":0,"data":{"80001":{"video_model":{}}}}`, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var cdid string
			tabRequests := 0
			d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
				query := request.URL.Query()
				if request.Method == http.MethodPost {
					cdid = query.Get("cdid")
					return sourceFixtureResponse(request, test.postStatus, test.postBody), nil
				}
				tabRequests++
				if request.Method != http.MethodGet || request.URL.Path != "/reading/bookapi/video_tab/multi_video_model/v1/" ||
					query.Get("video_id") != videoID || query.Get("device_id") != identity.DeviceID ||
					query.Get("iid") != identity.InstallID || cdid == "" || query.Get("cdid") != cdid || request.ContentLength != 0 {
					t.Error("tab fallback changed the episode or registered identity")
				}
				if request.Header.Get("X-SS-Req-Ticket") != query.Get("_rticket") || request.Header.Get("X-Medusa") == "" ||
					request.Header.Get("X-SS-Stub") != "" || request.Header.Get("Content-Type") != "" || request.Header.Get("Content-Encoding") != "" {
					t.Error("tab fallback did not use bodyless request headers")
				}
				return sourceFixtureResponse(request, 200, test.tabBody), nil
			})
			entry, err := d.hongguoV5PlayerPost(context.Background(), videoID, identity)
			if (err != nil) != test.wantError || (!test.wantError && entry["video_model"] == nil) {
				t.Fatalf("entry=%v err=%v", entry, err)
			}
			if wantTab := test.tabBody != ""; (tabRequests == 1) != wantTab || tabRequests > 1 {
				t.Fatalf("unexpected tab requests: %d", tabRequests)
			}
		})
	}
}
