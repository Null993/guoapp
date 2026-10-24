package core

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNativeStreamProxiesMP4WithPlaylistLikeQuery(t *testing.T) {
	payload := append([]byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2mp41"), bytes.Repeat([]byte{1, 2, 3, 4}, 8192)...)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/video" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Referer") != "https://example.test/" {
			t.Errorf("upstream lost referer: %q", r.Header.Get("Referer"))
		}
		w.Header().Set("Content-Type", "video/mp4")
		http.ServeContent(w, r, "video.mp4", time.Time{}, bytes.NewReader(payload))
	}))
	defer upstream.Close()

	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.downloads.close)
	stream, err := newNativeStreamServer(engine.downloader)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.server.Close()

	address, token := stream.nativeOpen(providerMedia{
		URL:     upstream.URL + "/video?rc=Mi1hLS1kXy9zcw%3D%3D&mime_type=video_mp4",
		CENCKey: []byte("0123456789abcdef"),
		Referer: "https://example.test/",
	})
	defer stream.nativeRelease(token)
	if !strings.HasSuffix(address, ".mp4") {
		t.Fatalf("MP4 直链未按 .mp4 注册: %s", address)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d，响应体 %q", response.StatusCode, body)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("透传内容不一致: got %d bytes, want %d", len(body), len(payload))
	}

	request, err := http.NewRequest(http.MethodGet, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Range", "bytes=16-115")
	ranged, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	partial, err := io.ReadAll(ranged.Body)
	ranged.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if ranged.StatusCode != http.StatusPartialContent {
		t.Fatalf("Range 状态码 %d", ranged.StatusCode)
	}
	if !bytes.Equal(partial, payload[16:116]) {
		t.Fatalf("Range 内容不一致: got %d bytes", len(partial))
	}
}

func TestNativeStreamDetectsPlaylistByContent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/play":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = io.WriteString(w, "#EXTM3U\n#EXTINF:1.0,\nsegment-1.ts\n")
		case "/segment-1.ts":
			w.Header().Set("Content-Type", "video/mp2t")
			_, _ = w.Write(bytes.Repeat([]byte{7}, 1024))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.downloads.close)
	stream, err := newNativeStreamServer(engine.downloader)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.server.Close()

	address, token := stream.nativeOpen(providerMedia{URL: upstream.URL + "/play", Referer: "https://example.test/"})
	defer stream.nativeRelease(token)

	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "#EXTM3U") {
		t.Fatalf("播放列表未被识别: status=%d body=%.120q", response.StatusCode, body)
	}
	if strings.Contains(string(body), "segment-1.ts") && !strings.Contains(string(body), "http://") {
		t.Fatalf("播放列表未重写: %s", body)
	}
}

func TestNativeStreamHeadProbesAmbiguousPlaylist(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/play" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, "#EXTM3U\n#EXTINF:1.0,\nsegment-1.ts\n")
	}))
	defer upstream.Close()

	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.downloads.close)
	stream, err := newNativeStreamServer(engine.downloader)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.server.Close()

	address, token := stream.nativeOpen(providerMedia{URL: upstream.URL + "/play", Referer: "https://example.test/"})
	defer stream.nativeRelease(token)

	client := &http.Client{Timeout: 10 * time.Second}
	request, err := http.NewRequest(http.MethodHead, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if got := response.Header.Get("Content-Type"); !strings.Contains(got, "mpegurl") {
		t.Fatalf("HEAD 未识别播放列表，Content-Type=%q", got)
	}
}
