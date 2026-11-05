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
