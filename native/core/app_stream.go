package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type nativeStreamAsset struct {
	address     string
	data        []byte
	contentType string
	total       int64
	etag        string
	modified    string
}

type nativeStreamSession struct {
	credentials *providerMediaCredentials
	mu          sync.Mutex
	assets      map[string]nativeStreamAsset
	referer     string
	key         []byte
	ctx         context.Context
	cancel      context.CancelFunc
	lastUsed    time.Time
	events      []string
	failures    []string
	requests    int
}

type nativeStreamServer struct {
	mu         sync.Mutex
	downloader *Downloader
	address    string
	sessions   map[string]*nativeStreamSession
	server     *http.Server
}

func (stream *nativeStreamServer) nativeRequest(request *http.Request) (*http.Response, error) {
	client := *stream.downloader.client
	client.Timeout = 0
	return stream.downloader.doMediaRequestWithClient(request, &client)
}

var nativePlaylistURI = regexp.MustCompile(`URI="([^"]+)"`)

const nativeStreamStatusID = "__status"

type nativeStreamRecorder struct {
	http.ResponseWriter
	status int
	detail string
	failed bool
}

func (recorder *nativeStreamRecorder) WriteHeader(status int) {
	if recorder.status == 0 {
		recorder.status = status
	}
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *nativeStreamRecorder) Write(data []byte) (int, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	return recorder.ResponseWriter.Write(data)
}

func (session *nativeStreamSession) record(failure bool, line string) {
	entry := "[" + time.Now().Format("15:04:05") + "] " + line
	session.mu.Lock()
	session.requests++
	if failure {
		session.failures = append(session.failures, entry)
		if len(session.failures) > 16 {
			session.failures = session.failures[len(session.failures)-16:]
		}
	}
	session.events = append(session.events, entry)
	if len(session.events) > 64 {
		session.events = session.events[len(session.events)-64:]
	}
	session.mu.Unlock()
}

func nativeStreamTarget(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return address
	}
	return parsed.Host + parsed.Path
}

func (stream *nativeStreamServer) nativeStatus(writer http.ResponseWriter, session *nativeStreamSession) {
	session.mu.Lock()
	requests := session.requests
	failures := append([]string{}, session.failures...)
	events := append([]string{}, session.events...)
	session.mu.Unlock()
	var builder strings.Builder
	fmt.Fprintf(&builder, "本机播放代理：请求 %d 次，失败 %d 次\n", requests, len(failures))
	builder.WriteString("失败记录：\n")
	if len(failures) == 0 {
		builder.WriteString("  无\n")
	}
	for _, line := range failures {
		builder.WriteString("  " + line + "\n")
	}
	builder.WriteString("最近请求：\n")
	for _, line := range events {
		builder.WriteString("  " + line + "\n")
	}
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(writer, builder.String())
}

func newNativeStreamServer(d *Downloader) (*nativeStreamServer, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("无法初始化本机播放器")
	}
	stream := &nativeStreamServer{downloader: d, address: "http://" + listener.Addr().String(), sessions: map[string]*nativeStreamSession{}}
	server := &http.Server{Handler: http.HandlerFunc(stream.nativeServe), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	stream.server = server
	go func() { _ = server.Serve(listener) }()
	return stream, nil
}

func (stream *nativeStreamServer) nativeOpen(media providerMedia) (string, string) {
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		panic(err)
	}
	token := hex.EncodeToString(tokenBytes)
	ctx, cancel := context.WithCancel(providerMediaContext(context.Background(), media.credentials))
	session := &nativeStreamSession{assets: map[string]nativeStreamAsset{}, referer: media.Referer, key: media.HLSKey, ctx: ctx, cancel: cancel, lastUsed: time.Now(), credentials: media.credentials}
	stream.mu.Lock()
	for id, old := range stream.sessions {
		if time.Since(old.lastUsed) > 10*time.Minute {
			old.cancel()
			delete(stream.sessions, id)
		}
	}
	if len(stream.sessions) >= 8 {
		oldest := ""
		for id, old := range stream.sessions {
			if oldest == "" || old.lastUsed.Before(stream.sessions[oldest].lastUsed) {
				oldest = id
			}
		}
		stream.sessions[oldest].cancel()
		delete(stream.sessions, oldest)
	}
	stream.sessions[token] = session
	stream.mu.Unlock()
	entry := nativeStreamAsset{address: media.URL, contentType: "video/mp4"}
	isHLS := media.Playlist != ""
	if parsed, err := url.Parse(media.URL); err == nil && strings.HasSuffix(strings.ToLower(parsed.Path), ".m3u8") {
		isHLS = true
	}
	if isHLS {
		entry.contentType = "application/vnd.apple.mpegurl"
	}
	if media.Playlist != "" {
		entry.data = []byte(media.Playlist)
		entry.contentType = "application/vnd.apple.mpegurl"
	}
	return stream.nativeAsset(token, session, entry), token
}

func (stream *nativeStreamServer) nativeRelease(token string) {
	stream.mu.Lock()
	if session := stream.sessions[token]; session != nil {
		session.cancel()
		delete(stream.sessions, token)
	}
	stream.mu.Unlock()
}

func (stream *nativeStreamServer) nativeAsset(token string, session *nativeStreamSession, asset nativeStreamAsset) string {
	digest := sha256.Sum256([]byte(asset.address + "\x00" + asset.contentType))
	extension := ".ts"
	if parsed, err := url.Parse(asset.address); err == nil {
		switch candidate := strings.ToLower(path.Ext(parsed.Path)); candidate {
		case ".m3u8", ".ts", ".m4s", ".mp4", ".aac", ".m4a", ".mp3", ".vtt", ".webvtt", ".key":
			extension = candidate
		}
	}
	switch asset.contentType {
	case "application/vnd.apple.mpegurl":
		extension = ".m3u8"
	case "application/octet-stream":
		extension = ".key"
	case "video/mp4":
		extension = ".mp4"
	}
	id := hex.EncodeToString(digest[:12]) + extension
	session.mu.Lock()
	if previous, found := session.assets[id]; !found || len(previous.data) == 0 || len(asset.data) > 0 {
		session.assets[id] = asset
	}
	session.mu.Unlock()
	return stream.address + "/" + token + "/" + id
}

func (stream *nativeStreamServer) nativeRewrite(token string, session *nativeStreamSession, body, base string) (string, error) {
	var output []string
	nextPlaylist := false
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		text := strings.TrimSpace(line)
		rewrite := func(reference string, key bool, contentType string) string {
			if strings.HasPrefix(reference, "data:") {
				return reference
			}
			parsed, err := url.Parse(base)
			if err != nil {
				return ""
			}
			relative, err := url.Parse(reference)
			if err != nil {
				return ""
			}
			address := parsed.ResolveReference(relative).String()
			if !isProviderHTTPMediaURL(address) {
				return ""
			}
			asset := nativeStreamAsset{address: address, contentType: contentType}
			if key && len(session.key) == 16 {
				asset.data = append([]byte{}, session.key...)
				asset.contentType = "application/octet-stream"
			}
			return stream.nativeAsset(token, session, asset)
		}
		if text != "" && !strings.HasPrefix(text, "#") {
			contentType := ""
			if nextPlaylist {
				contentType = "application/vnd.apple.mpegurl"
			}
			if updated := rewrite(text, false, contentType); updated != "" {
				line = updated
			}
			nextPlaylist = false
		} else if strings.Contains(text, "URI=") {
			line = nativePlaylistURI.ReplaceAllStringFunc(line, func(match string) string {
				reference := nativePlaylistURI.FindStringSubmatch(match)[1]
				key := strings.HasPrefix(text, "#EXT-X-KEY:") || strings.HasPrefix(text, "#EXT-X-SESSION-KEY:")
				contentType := ""
				switch {
				case key:
					contentType = "application/octet-stream"
				case strings.HasPrefix(text, "#EXT-X-MAP:"):
					contentType = "video/mp4"
				case strings.HasPrefix(text, "#EXT-X-MEDIA:"), strings.HasPrefix(text, "#EXT-X-I-FRAME-STREAM-INF:"), strings.HasPrefix(text, "#EXT-X-RENDITION-REPORT:"):
					contentType = "application/vnd.apple.mpegurl"
				}
				if updated := rewrite(reference, key, contentType); updated != "" {
					return "URI=\"" + updated + "\""
				}
				return match
			})
		}
		if strings.HasPrefix(text, "#EXT-X-STREAM-INF:") {
			nextPlaylist = true
		}
		output = append(output, line)
	}
	return strings.Join(output, "\n"), nil
}

func (stream *nativeStreamServer) nativeProbePlaylist(ctx context.Context, session *nativeStreamSession, address string) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return false
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Referer", session.referer)
	request.Header.Set("Range", "bytes=0-511")
	response, err := stream.nativeRequest(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		return false
	}
	peek, _ := io.ReadAll(io.LimitReader(response.Body, 512))
	text := strings.TrimSpace(strings.TrimPrefix(string(peek), "\ufeff"))
	return strings.HasPrefix(text, "#EXTM3U")
}

func (stream *nativeStreamServer) nativeServe(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) != 2 {
		http.NotFound(writer, request)
		return
	}
	stream.mu.Lock()
	session := stream.sessions[parts[0]]
	if session != nil {
		session.lastUsed = time.Now()
	}
	stream.mu.Unlock()
	if session == nil {
		http.Error(writer, "播放已结束", http.StatusGone)
		return
	}
	if parts[1] == nativeStreamStatusID {
		stream.nativeStatus(writer, session)
		return
	}
	recorder := &nativeStreamRecorder{ResponseWriter: writer}
	writer = recorder
	defer func() {
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		session.record(status >= 400 || recorder.failed, request.Method+" /"+parts[1]+" -> "+strconv.Itoa(status)+recorder.detail)
	}()
	session.mu.Lock()
	asset, found := session.assets[parts[1]]
	session.mu.Unlock()
	if !found {
		recorder.detail = " 会话中没有该资源"
		http.NotFound(writer, request)
		return
	}
	ctx, cancel := context.WithCancel(providerMediaContext(request.Context(), session.credentials))
	defer cancel()
	stop := context.AfterFunc(session.ctx, cancel)
	defer stop()
	writer.Header().Set("Cache-Control", "no-store")
	if len(asset.data) > 0 && asset.total > int64(len(asset.data)) {
		if stream.nativeServePrefix(ctx, writer, request, session, asset) {
			recorder.detail = " 预取数据 " + strconv.FormatInt(asset.total, 10) + " 字节"
			return
		}
		asset.data = nil
	}
	if len(asset.data) > 0 {
		if strings.Contains(asset.contentType, "mpegurl") {
			body, err := stream.nativeRewrite(parts[0], session, string(asset.data), asset.address)
			if err != nil {
				recorder.detail = " 会话播放列表重写失败: " + err.Error()
				http.Error(writer, err.Error(), http.StatusBadGateway)
				return
			}
			writer.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			if request.Method == http.MethodGet {
				_, _ = io.WriteString(writer, body)
			}
			recorder.detail = " 会话播放列表 " + strconv.Itoa(len(body)) + " 字节"
			return
		}
		writer.Header().Set("Content-Type", asset.contentType)
		if asset.etag != "" {
			writer.Header().Set("ETag", asset.etag)
		}
		recorder.detail = " 会话资源 " + nativeStreamTarget(asset.address)
		http.ServeContent(writer, request, parts[1], time.Time{}, bytes.NewReader(asset.data))
		return
	}
	upstream, err := http.NewRequestWithContext(ctx, request.Method, asset.address, nil)
	if err != nil {
		recorder.detail = " 媒体地址无效"
		http.Error(writer, "媒体地址无效", http.StatusBadGateway)
		return
	}
	upstream.Header.Set("User-Agent", userAgent)
	upstream.Header.Set("Referer", session.referer)
	for _, name := range []string{"Range", "If-Range"} {
		if value := request.Header.Get(name); value != "" {
			upstream.Header.Set(name, value)
		}
	}
	response, err := stream.nativeRequest(upstream)
	if err != nil {
		recorder.detail = " 读取上游失败: " + err.Error()
		http.Error(writer, "读取媒体失败，请重试", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	target := " 上游 " + nativeStreamTarget(asset.address)
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		failure := stream.downloader.catalogResponseError(upstream, response, body)
		recorder.detail = target + " " + strconv.Itoa(response.StatusCode) + ": " + failure.Error()
		http.Error(writer, failure.Error(), response.StatusCode)
		return
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	finalURL := upstream.URL
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL
	}
	if final := nativeStreamTarget(finalURL.String()); final != "" && final != nativeStreamTarget(asset.address) {
		target = " 上游 " + final
	}
	contentIsMP4 := strings.Contains(contentType, "video/mp4")
	playlistHint := strings.Contains(contentType, "mpegurl") || (!contentIsMP4 &&
		(strings.Contains(asset.contentType, "mpegurl") || strings.HasSuffix(strings.ToLower(finalURL.Path), ".m3u8")))
	reader := bufio.NewReader(response.Body)
	playlist := playlistHint
	if request.Method == http.MethodGet {
		peek, _ := reader.Peek(16)
		playlist = strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(string(peek), "\ufeff")), "#EXTM3U")
		if playlistHint && !playlist {
			recorder.detail = target + " 未返回播放列表"
		}
	}
	if request.Method == http.MethodHead && !playlist &&
		(contentType == "" || strings.Contains(contentType, "octet-stream") || strings.Contains(contentType, "text/plain")) {
		playlist = stream.nativeProbePlaylist(ctx, session, asset.address)
	}
	if playlist && request.Method == http.MethodHead {
		writer.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		writer.WriteHeader(http.StatusOK)
		recorder.detail = target + " 播放列表探测"
		return
	}
	if playlist && request.Method == http.MethodGet {
		body, err := io.ReadAll(io.LimitReader(reader, 4<<20+1))
		if err != nil || len(body) > 4<<20 {
			recorder.detail = target + " 播放列表过大或读取失败"
			http.Error(writer, "播放列表过大或读取失败", http.StatusBadGateway)
			return
		}
		text := strings.TrimSpace(strings.TrimPrefix(string(body), "\ufeff"))
		rewritten, err := stream.nativeRewrite(parts[0], session, text, finalURL.String())
		if err != nil {
			recorder.detail = target + " 播放列表重写失败: " + err.Error()
			http.Error(writer, err.Error(), http.StatusBadGateway)
			return
		}
		writer.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = io.WriteString(writer, rewritten)
		recorder.detail = target + " 播放列表 " + strconv.Itoa(len(rewritten)) + " 字节"
		return
	}
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if value := response.Header.Get(name); value != "" {
			writer.Header().Set(name, value)
		}
	}
	writer.WriteHeader(response.StatusCode)
	recorder.detail = target + " " + response.Header.Get("Content-Length") + " 字节"
	if request.Method == http.MethodGet {
		pointer := nativeStreamCopyPool.Get().(*[]byte)
		written, copyErr := io.CopyBuffer(struct{ io.Writer }{writer}, struct{ io.Reader }{reader}, *pointer)
		nativeStreamCopyPool.Put(pointer)
		if copyErr != nil && request.Context().Err() == nil && session.ctx.Err() == nil {
			recorder.failed = true
			recorder.detail += " 转发中断（已发送 " + strconv.FormatInt(written, 10) + " 字节）: " + publicError(copyErr).Error()
		}
	}
}

const nativeStreamCopyBuffer = 128 << 10

var nativeStreamCopyPool = sync.Pool{New: func() any {
	buffer := make([]byte, nativeStreamCopyBuffer)
	return &buffer
}}
