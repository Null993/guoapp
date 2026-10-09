package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	syntheticStreamReferer = "https://source.example.test/watch/1"
	syntheticStreamKey     = "synthetic-key-16"
)

const syntheticMasterPlaylist = "#EXTM3U\n" +
	"#EXT-X-VERSION:7\n" +
	"#EXT-X-STREAM-INF:BANDWIDTH=1200000,RESOLUTION=1080x1920\n" +
	"variant/index.m3u8\n"

const syntheticVariantPlaylist = "#EXTM3U\n" +
	"#EXT-X-VERSION:7\n" +
	"#EXT-X-TARGETDURATION:4\n" +
	"#EXT-X-MEDIA-SEQUENCE:0\n" +
	"#EXT-X-MAP:URI=\"../init.mp4\"\n" +
	"#EXT-X-KEY:METHOD=AES-128,URI=\"../media.key\",IV=0x00000000000000000000000000000001\n" +
	"#EXTINF:4.0,\n" +
	"../segments/0.ts\n" +
	"#EXTINF:4.0,\n" +
	"../segments/1.ts\n" +
	"#EXTINF:2.0,\n" +
	"#EXT-X-BYTERANGE:512@0\n" +
	"../segments/2.ts\n" +
	"#EXT-X-ENDLIST\n"

const syntheticBrokenPlaylist = "#EXTM3U\n" +
	"#EXT-X-VERSION:7\n" +
	"#EXT-X-TARGETDURATION:4\n" +
	"#EXT-X-DEFINE:NAME=\"prefix\",VALUE=\"../segments/\"\n" +
	"#EXTINF:4.0,\n" +
	"../segments/0.ts\n" +
	"#EXTINF:4.0,\n" +
	"{$prefix}bad%zz.ts\n" +
	"#EXT-X-ENDLIST\n"

type syntheticHLSSource struct {
	mu       sync.Mutex
	hits     map[string]int
	server   *httptest.Server
	key      []byte
	segments map[string][]byte
}

func syntheticBytes(seed byte, size int) []byte {
	data := make([]byte, size)
	for index := range data {
		data[index] = seed + byte(index%97)
	}
	return data
}

func (source *syntheticHLSSource) track(path string) {
	source.mu.Lock()
	source.hits[path]++
	source.mu.Unlock()
}

func (source *syntheticHLSSource) count(path string) int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.hits[path]
}

func syntheticRangeResponse(t *testing.T, writer http.ResponseWriter, request *http.Request, body []byte, contentType string) {
	t.Helper()
	writer.Header().Set("Accept-Ranges", "bytes")
	writer.Header().Set("Content-Type", contentType)
	value := request.Header.Get("Range")
	if value == "" {
		writer.Header().Set("Content-Length", strconv.Itoa(len(body)))
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(body)
		return
	}
	if !strings.HasPrefix(value, "bytes=") {
		writer.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	fields := strings.SplitN(strings.TrimPrefix(value, "bytes="), "-", 2)
	start, err := strconv.Atoi(fields[0])
	if err != nil || start < 0 || start >= len(body) {
		writer.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	end := len(body) - 1
	if len(fields) == 2 && fields[1] != "" {
		parsed, err := strconv.Atoi(fields[1])
		if err != nil || parsed < start {
			writer.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		end = min(parsed, len(body)-1)
	}
	writer.Header().Set("Content-Range", "bytes "+strconv.Itoa(start)+"-"+strconv.Itoa(end)+"/"+strconv.Itoa(len(body)))
	writer.Header().Set("Content-Length", strconv.Itoa(end-start+1))
	writer.WriteHeader(http.StatusPartialContent)
	_, _ = writer.Write(body[start : end+1])
}

func newSyntheticHLSSource(t *testing.T) *syntheticHLSSource {
	t.Helper()
	source := &syntheticHLSSource{
		hits: map[string]int{},
		key:  []byte(syntheticStreamKey),
		segments: map[string][]byte{
			"/init.mp4":      syntheticBytes(0x44, 1024),
			"/segments/0.ts": syntheticBytes(0x11, 700),
			"/segments/1.ts": syntheticBytes(0x22, 900),
			"/segments/2.ts": syntheticBytes(0x33, 512),
			"/media/stream":  syntheticBytes(0x55, 1024),
		},
	}
	source.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		source.track(request.URL.Path)
		if request.Header.Get("Referer") != syntheticStreamReferer {
			t.Errorf("上游请求丢失站点 Referer: %s", request.URL.Path)
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		switch request.URL.Path {
		case "/master.m3u8":
			writer.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(writer, syntheticMasterPlaylist)
		case "/variant/index.m3u8":
			writer.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(writer, syntheticVariantPlaylist)
		case "/broken/index.m3u8":
			writer.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(writer, syntheticBrokenPlaylist)
		case "/media.key":
			writer.Header().Set("Content-Type", "application/octet-stream")
			_, _ = writer.Write(source.key)
		case "/media/stream":
			syntheticRangeResponse(t, writer, request, source.segments["/media/stream"], "video/mp4")
		default:
			body, found := source.segments[request.URL.Path]
			if !found {
				writer.WriteHeader(http.StatusNotFound)
				return
			}
			syntheticRangeResponse(t, writer, request, body, "video/mp2t")
		}
	}))
	t.Cleanup(source.server.Close)
	return source
}

type syntheticStreamResponse struct {
	status      int
	contentType string
	header      http.Header
	body        []byte
}

func syntheticStreamGet(t *testing.T, address, rangeValue string) syntheticStreamResponse {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rangeValue != "" {
		request.Header.Set("Range", rangeValue)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return syntheticStreamResponse{status: response.StatusCode, contentType: response.Header.Get("Content-Type"), header: response.Header, body: body}
}

func syntheticStreamServer(t *testing.T) (*nativeStreamServer, *Downloader) {
	t.Helper()
	downloader := sourceFixtureDownloader(t, nil)
	downloader.client.Transport = nil
	stream, err := newNativeStreamServer(downloader)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.server.Close() })
	return stream, downloader
}

func syntheticPlaylistReferences(t *testing.T, playlist string) (string, string, []string, []string) {
	t.Helper()
	var key, mapping string
	var segments, ranges []string
	pending := ""
	for _, raw := range strings.Split(playlist, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			match := nativePlaylistURI.FindStringSubmatch(line)
			if len(match) != 2 {
				t.Fatalf("EXT-X-MAP 缺少 URI: %q", line)
			}
			mapping = match[1]
		case strings.HasPrefix(line, "#EXT-X-KEY:"):
			match := nativePlaylistURI.FindStringSubmatch(line)
			if len(match) != 2 {
				t.Fatalf("EXT-X-KEY 缺少 URI: %q", line)
			}
			key = match[1]
		case strings.HasPrefix(line, "#EXT-X-BYTERANGE:"):
			pending = strings.TrimPrefix(line, "#EXT-X-BYTERANGE:")
		case line != "" && !strings.HasPrefix(line, "#"):
			segments = append(segments, line)
			ranges = append(ranges, pending)
			pending = ""
		}
	}
	return key, mapping, segments, ranges
}

func syntheticPlayerWalk(t *testing.T, stream *nativeStreamServer, address, token string, source *syntheticHLSSource, expectedKey []byte, upstreamKey bool) string {
	t.Helper()
	local := stream.address + "/" + token + "/"
	master := syntheticStreamGet(t, address, "")
	if master.status != http.StatusOK || !strings.HasPrefix(string(master.body), "#EXTM3U") || !strings.Contains(master.contentType, "mpegurl") {
		t.Fatalf("主播放列表不可用: %d %s %q", master.status, master.contentType, string(master.body))
	}
	if strings.Contains(string(master.body), source.server.URL) {
		t.Fatal("主播放列表仍指向上游地址")
	}
	variant := ""
	for _, raw := range strings.Split(string(master.body), "\n") {
		line := strings.TrimSpace(raw)
		if line != "" && !strings.HasPrefix(line, "#") {
			variant = line
			break
		}
	}
	if !strings.HasPrefix(variant, local) {
		t.Fatalf("分播放列表未改写为本地代理地址: %q", variant)
	}
	media := syntheticStreamGet(t, variant, "")
	if media.status != http.StatusOK || !strings.Contains(media.contentType, "mpegurl") {
		t.Fatalf("分播放列表不可用: %d %s %q", media.status, media.contentType, string(media.body))
	}
	keyURI, mapURI, segments, ranges := syntheticPlaylistReferences(t, string(media.body))
	if keyURI == "" || mapURI == "" || len(segments) != 3 {
		t.Fatalf("分播放列表结构不完整: key=%q map=%q segments=%d", keyURI, mapURI, len(segments))
	}
	if upstreamKey && source.count("/media.key") != 0 {
		t.Fatal("会话密钥已注入却仍回源读取密钥")
	}
	key := syntheticStreamGet(t, keyURI, "")
	if key.status != http.StatusOK || len(key.body) != 16 || string(key.body) != string(expectedKey) {
		t.Fatalf("播放密钥不正确: %d %q", key.status, string(key.body))
	}
	mapping := syntheticStreamGet(t, mapURI, "bytes=0-")
	if mapping.status != http.StatusPartialContent || mapping.header.Get("Content-Range") != "bytes 0-1023/1024" || !strings.EqualFold(string(mapping.body), string(source.segments["/init.mp4"])) {
		t.Fatalf("EXT-X-MAP 未按 Range 转发: %d %q", mapping.status, mapping.header.Get("Content-Range"))
	}
	upstreamPaths := []string{"/segments/0.ts", "/segments/1.ts", "/segments/2.ts"}
	for index, segment := range segments {
		if !strings.HasPrefix(segment, local) {
			t.Fatalf("分片未改写为本地代理地址: %q", segment)
		}
		offset, length := 0, len(source.segments[upstreamPaths[index]])
		rangeValue := "bytes=0-"
		if ranges[index] != "" {
			fields := strings.SplitN(ranges[index], "@", 2)
			length, _ = strconv.Atoi(fields[0])
			if len(fields) == 2 {
				offset, _ = strconv.Atoi(fields[1])
			}
			rangeValue = "bytes=" + strconv.Itoa(offset) + "-" + strconv.Itoa(offset+length-1)
		}
		response := syntheticStreamGet(t, segment, rangeValue)
		if response.status != http.StatusPartialContent {
			t.Fatalf("分片 %d 未按 Range 返回 206: %d %s", index, response.status, string(response.body))
		}
		want := source.segments[upstreamPaths[index]][offset : offset+length]
		if string(response.body) != string(want) {
			t.Fatalf("分片 %d 内容与上游不一致: %d 字节", index, len(response.body))
		}
	}
	status := syntheticStreamGet(t, local+"__status", "")
	if status.status != http.StatusOK || !strings.Contains(string(status.body), "失败 0 次") {
		t.Fatalf("本机代理诊断不可用: %d %q", status.status, string(status.body))
	}
	return variant
}

func TestNativeStreamProxyServesSyntheticSourceHLS(t *testing.T) {
	source := newSyntheticHLSSource(t)
	stream, _ := syntheticStreamServer(t)
	media := providerMedia{URL: source.server.URL + "/master.m3u8", Playlist: syntheticMasterPlaylist,
		Referer: syntheticStreamReferer, CENCKey: []byte(syntheticStreamKey)}
	address, token := stream.nativeOpen(media)
	defer stream.nativeRelease(token)
	syntheticPlayerWalk(t, stream, address, token, source, source.key, false)
	if source.count("/variant/index.m3u8") == 0 || source.count("/segments/0.ts") == 0 {
		t.Fatal("合成播放未经过上游")
	}
}

func TestNativeStreamProxyServesLivePlaylistsWithSessionKey(t *testing.T) {
	source := newSyntheticHLSSource(t)
	stream, _ := syntheticStreamServer(t)
	media := providerMedia{URL: source.server.URL + "/master.m3u8", Referer: syntheticStreamReferer,
		HLSKey: []byte(syntheticStreamKey)}
	address, token := stream.nativeOpen(media)
	defer stream.nativeRelease(token)
	syntheticPlayerWalk(t, stream, address, token, source, source.key, true)
}

func TestNativeStreamProxyKeepsUnresolvablePlaylistReference(t *testing.T) {
	source := newSyntheticHLSSource(t)
	stream, _ := syntheticStreamServer(t)
	media := providerMedia{URL: source.server.URL + "/broken/index.m3u8", Playlist: syntheticBrokenPlaylist, Referer: syntheticStreamReferer}
	address, token := stream.nativeOpen(media)
	defer stream.nativeRelease(token)
	playlist := syntheticStreamGet(t, address, "")
	if playlist.status != http.StatusOK || !strings.HasPrefix(string(playlist.body), "#EXTM3U") {
		t.Fatalf("坏行导致整份播放列表失败: %d %q", playlist.status, string(playlist.body))
	}
	if !strings.Contains(string(playlist.body), "{$prefix}bad%zz.ts") {
		t.Fatalf("无法改写的行未按原样保留: %q", string(playlist.body))
	}
	if !strings.Contains(string(playlist.body), stream.address+"/"+token+"/") {
		t.Fatalf("可改写的行未改写为本地代理地址: %q", string(playlist.body))
	}
}

func TestNativeStreamProxyRelaysMediaWhoseURLMentionsPlaylist(t *testing.T) {
	for _, query := range []string{"?format=m3u8", "?hls=1&mime_type=video_mp4"} {
		t.Run(query, func(t *testing.T) {
			source := newSyntheticHLSSource(t)
			stream, _ := syntheticStreamServer(t)
			media := providerMedia{URL: source.server.URL + "/media/stream" + query, Referer: syntheticStreamReferer, CENCKey: []byte(syntheticStreamKey)}
			address, token := stream.nativeOpen(media)
			defer stream.nativeRelease(token)
			if !strings.HasSuffix(address, ".mp4") {
				t.Fatalf("上游媒体被误标为播放列表地址: %s", address)
			}
			full := syntheticStreamGet(t, address, "")
			if full.status != http.StatusOK || string(full.body) != string(source.segments["/media/stream"]) || !strings.Contains(full.contentType, "video/mp4") {
				t.Fatalf("URL 含播放列表字样的媒体被误判为播放列表: %d %s %q", full.status, full.contentType, string(full.body))
			}
			partial := syntheticStreamGet(t, address, "bytes=0-99")
			if partial.status != http.StatusPartialContent || partial.header.Get("Content-Range") != "bytes 0-99/1024" || len(partial.body) != 100 {
				t.Fatalf("Range 未按原样转发: %d %q %d", partial.status, partial.header.Get("Content-Range"), len(partial.body))
			}
		})
	}
}

func liveRedact(value string) string {
	trimmed := strings.TrimSpace(value)
	if index := strings.Index(trimmed, "?"); index >= 0 {
		return trimmed[:index] + "?…"
	}
	return trimmed
}

func livePlayerFetch(ctx context.Context, t *testing.T, client *http.Client, address, rangeValue string) (int, http.Header, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		t.Fatalf("请求地址无效: %v", err)
	}
	if rangeValue != "" {
		request.Header.Set("Range", rangeValue)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", liveRedact(address), err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	return response.StatusCode, response.Header, body
}

func TestLiveHongguoPlaybackProxyWalk(t *testing.T) {
	if os.Getenv("CHECK_LIVE_HONGGUO") != "true" {
		t.Skip("set CHECK_LIVE_HONGGUO=true to touch the live 红果 source")
	}
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.downloads.close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	page, err := engine.nativeCatalog(ctx, nativeInput{Source: sourceHongguo, Page: 1, Force: true})
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("红果目录不可用: count=%d err=%v", len(page.Items), err)
	}
	t.Logf("红果目录解析 %d 部剧", len(page.Items))
	var drama nativeDrama
	var chapter Chapter
	for _, item := range page.Items {
		if item.VIP != nil && *item.VIP {
			continue
		}
		_, id, ok := splitProviderDramaID(item.ID)
		if !ok {
			continue
		}
		_, chapters, err := engine.downloader.GetHuangguoChapters(ctx, sourceHongguo, id)
		if err != nil {
			continue
		}
		for _, candidate := range chapters {
			if !candidate.VIP {
				drama, chapter = item, candidate
				break
			}
		}
		if chapter.ID != "" {
			break
		}
	}
	if chapter.ID == "" {
		t.Fatal("没有找到可播放的免费分集")
	}
	media, err := engine.downloader.resolveProviderMedia(ctx, Task{DramaID: drama.ID, Chapter: chapter, Index: 1})
	if err != nil {
		t.Fatalf("红果取流失败: %v", err)
	}
	t.Logf("媒体地址 %s 内嵌播放列表=%t HLSKey=%d CENCKey=%d 清晰度=%d", liveRedact(media.URL), media.Playlist != "", len(media.HLSKey), len(media.CENCKey), media.Quality)
	if media.Playlist != "" {
		lines := strings.Split(media.Playlist, "\n")
		t.Logf("内嵌播放列表 %d 行", len(lines))
		for _, line := range lines {
			text := strings.TrimSpace(line)
			if text == "" || strings.HasPrefix(text, "#EXTINF") {
				continue
			}
			t.Logf("  播放列表: %s", liveRedact(text))
		}
	}
	choice := nativePlaybackChoices(media, 0)
	if len(choice.media) == 0 {
		t.Fatal("红果没有可用线路")
	}
	plan, err := engine.nativeOpenPlayback(ctx, choice)
	if err != nil {
		t.Fatalf("本机代理会话创建失败: %v", err)
	}
	defer engine.nativeReleasePlayback(plan.Session)
	t.Logf("本机代理地址 %s 线路=%d/%d 解密密钥=%t", plan.URL, plan.RouteIndex+1, plan.RouteCount, plan.Key != "")
	client := &http.Client{Timeout: 30 * time.Second}
	status, headers, body := livePlayerFetch(ctx, t, client, plan.URL, "")
	t.Logf("播放器 GET 主地址 -> %d %s %d 字节", status, headers.Get("Content-Type"), len(body))
	if status != http.StatusOK {
		t.Fatalf("主地址不可用: %d %q", status, string(body))
	}
	cursor, depth := plan.URL, 0
	for strings.HasPrefix(strings.TrimSpace(string(body)), "#EXTM3U") && depth < 4 {
		next := ""
		for _, line := range strings.Split(string(body), "\n") {
			text := strings.TrimSpace(line)
			if text != "" && !strings.HasPrefix(text, "#") {
				next = text
				break
			}
		}
		if next == "" {
			break
		}
		t.Logf("  播放器跟随播放列表 -> %s", liveRedact(next))
		cursor = next
		status, headers, body = livePlayerFetch(ctx, t, client, cursor, "")
		t.Logf("  播放器 GET -> %d %s %d 字节", status, headers.Get("Content-Type"), len(body))
		if status != http.StatusOK {
			t.Fatalf("播放列表 %s 不可用: %d %q", liveRedact(cursor), status, string(body))
		}
		depth++
	}
	if strings.HasPrefix(strings.TrimSpace(string(body)), "#EXTM3U") {
		references := nativePlaylistURI.FindAllStringSubmatch(string(body), -1)
		for _, reference := range references {
			code, referenceHeaders, referenceBody := livePlayerFetch(ctx, t, client, reference[1], "")
			t.Logf("  播放器 GET 附属资源 -> %d %s %d 字节", code, referenceHeaders.Get("Content-Type"), len(referenceBody))
		}
		for _, line := range strings.Split(string(body), "\n") {
			text := strings.TrimSpace(line)
			if text == "" || strings.HasPrefix(text, "#") {
				continue
			}
			code, segmentHeaders, segmentBody := livePlayerFetch(ctx, t, client, text, "bytes=0-1023")
			t.Logf("  播放器 GET 分片 -> %d %s %s %d 字节", code, segmentHeaders.Get("Content-Range"), segmentHeaders.Get("Content-Type"), len(segmentBody))
			if code != http.StatusPartialContent && code != http.StatusOK {
				t.Fatalf("分片 %s 不可用: %d %q", liveRedact(text), code, string(segmentBody))
			}
			break
		}
	} else {
		code, rangeHeaders, rangeBody := livePlayerFetch(ctx, t, client, cursor, "bytes=0-1023")
		t.Logf("播放器 Range GET -> %d %s %d 字节", code, rangeHeaders.Get("Content-Range"), len(rangeBody))
	}
	diagnostics := plan.URL
	if index := strings.LastIndex(diagnostics, "/"); index > 0 {
		diagnostics = diagnostics[:index] + "/" + nativeStreamStatusID
	}
	code, _, trail := livePlayerFetch(ctx, t, client, diagnostics, "")
	t.Logf("本机代理诊断 -> %d\n%s", code, string(trail))
}
