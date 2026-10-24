package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

const (
	routeLatencyTTL       = 30 * time.Minute
	routeProbeTimeout     = 3 * time.Second
	routeProbeBudget      = 900 * time.Millisecond
	routeProbeConcurrency = 4
	routeProbeBytes       = 1024
	routeLatencyMaxEntry  = 512
)

type routeLatencyEntry struct {
	rtt      time.Duration
	measured time.Time
}

func nativeRouteKey(media providerMedia) string {
	return media.URL + "\x00" + media.Referer
}

func (engine *nativeEngine) routeLatency(key string) (time.Duration, bool) {
	if engine == nil || key == "" {
		return 0, false
	}
	engine.routeLatencyMu.Lock()
	defer engine.routeLatencyMu.Unlock()
	entry, ok := engine.routeLatencies[key]
	if !ok || entry.rtt <= 0 || time.Since(entry.measured) > routeLatencyTTL {
		return 0, false
	}
	return entry.rtt, true
}

func (engine *nativeEngine) recordRouteLatency(key string, rtt time.Duration) {
	if engine == nil || key == "" || rtt <= 0 {
		return
	}
	engine.routeLatencyMu.Lock()
	defer engine.routeLatencyMu.Unlock()
	if engine.routeLatencies == nil {
		engine.routeLatencies = map[string]routeLatencyEntry{}
	}
	if previous, ok := engine.routeLatencies[key]; ok && previous.rtt > 0 && time.Since(previous.measured) <= routeLatencyTTL {
		rtt = (previous.rtt*2 + rtt) / 3
	}
	engine.routeLatencies[key] = routeLatencyEntry{rtt: rtt, measured: time.Now()}
	if len(engine.routeLatencies) > routeLatencyMaxEntry {
		oldest := key
		for candidate, entry := range engine.routeLatencies {
			if entry.measured.Before(engine.routeLatencies[oldest].measured) {
				oldest = candidate
			}
		}
		delete(engine.routeLatencies, oldest)
	}

}

func (engine *nativeEngine) measureRoute(ctx context.Context, media providerMedia, timeout time.Duration) time.Duration {
	if engine == nil || engine.downloader == nil || !isProviderHTTPMediaURL(media.URL) {
		return 0
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, media.URL, nil)
	if err != nil {
		return 0
	}
	mediaRequestHeaders(request, media.Referer)
	request.Header.Set("Range", "bytes=0-1023")
	if media.credentials != nil {
		if err := media.credentials.apply(request); err != nil {
			return 0
		}
	}
	client := *engine.downloader.client
	client.Timeout = timeout
	client.Jar = nil
	start := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return 0
	}
	defer response.Body.Close()
	_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, routeProbeBytes))
	if readErr != nil {
		return 0
	}
	elapsed := time.Since(start)
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		return 0
	}
	return elapsed
}

func (engine *nativeEngine) probeRoutes(ctx context.Context, choice nativePlaybackChoice, budget time.Duration) {
	if engine == nil || len(choice.media) < 2 || budget <= 0 {
		return
	}
	quality := choice.media[0].Quality
	jobs := make(chan providerMedia, min(len(choice.media), 32))
	seen := map[string]bool{}
	for _, media := range choice.media {
		if media.Quality != quality {
			continue
		}
		key := nativeRouteKey(media)
		if seen[key] {
			continue
		}
		if _, ok := engine.routeLatency(key); ok {
			continue
		}
		seen[key] = true
		jobs <- media
		if len(jobs) == cap(jobs) {
			break
		}
	}
	close(jobs)
	if len(jobs) == 0 {
		return
	}
	work, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var workers sync.WaitGroup
	workerCount := min(routeProbeConcurrency, len(jobs))
	for worker := 0; worker < workerCount; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for media := range jobs {
				if work.Err() != nil {
					return
				}
				if rtt := engine.measureRoute(work, media, routeProbeTimeout); rtt > 0 {
					engine.recordRouteLatency(nativeRouteKey(media), rtt)
				}
			}
		}()
	}
	workers.Wait()
}

func (engine *nativeEngine) nativeRankRoutes(choice *nativePlaybackChoice) {
	if engine == nil || choice == nil || len(choice.media) < 2 {
		return
	}
	latency := make([]time.Duration, len(choice.media))
	known := false
	for index, media := range choice.media {
		if value, ok := engine.routeLatency(nativeRouteKey(media)); ok {
			latency[index] = value
			known = true
		} else {
			latency[index] = 0
		}
	}
	if !known {
		return
	}
	order := make([]int, len(choice.media))
	for index := range order {
		order[index] = index
	}
	sort.SliceStable(order, func(i, j int) bool {
		left, right := choice.media[order[i]], choice.media[order[j]]
		if left.Quality != right.Quality {
			return left.Quality > right.Quality
		}
		leftLatency, rightLatency := latency[order[i]], latency[order[j]]
		if leftLatency == 0 || rightLatency == 0 {
			return leftLatency > 0 && rightLatency == 0
		}
		return leftLatency < rightLatency
	})
	ranked := make([]providerMedia, 0, len(choice.media))
	for _, index := range order {
		ranked = append(ranked, choice.media[index])
	}
	choice.media = ranked
}

type nativePlaybackChoice struct {
	danmakuSeries string
	danmakuVideo  string
	media         []providerMedia
	index         int
	qualities     []int
	streamSession string
	created       time.Time
}

func nativePlaybackChoices(media providerMedia, quality int) nativePlaybackChoice {
	choice := nativePlaybackChoice{qualities: []int{}}
	seen := map[string]bool{}
	qualities := map[int]bool{}
	for _, option := range append([]providerMedia{media}, media.Variants...) {
		if !isProviderHTTPMediaURL(option.URL) {
			continue
		}
		identity := option.URL + "\x00" + option.Referer + "\x00" + hex.EncodeToString(option.CENCKey) + "\x00" + hex.EncodeToString(option.HLSKey)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		option.Variants = nil
		choice.media = append(choice.media, option)
		if option.Quality > 0 {
			qualities[option.Quality] = true
		}
	}
	sort.SliceStable(choice.media, func(i, j int) bool { return choice.media[i].Quality > choice.media[j].Quality })
	for value := range qualities {
		choice.qualities = append(choice.qualities, value)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(choice.qualities)))
	if quality > 0 && qualities[quality] {
		filtered := make([]providerMedia, 0, len(choice.media))
		for _, option := range choice.media {
			if option.Quality == quality {
				filtered = append(filtered, option)
			}
		}
		choice.media = filtered
	}
	return choice
}

func (engine *nativeEngine) nativeOpenPlayback(ctx context.Context, choice nativePlaybackChoice) (nativePlan, error) {
	if err := ctx.Err(); err != nil {
		return nativePlan{}, err
	}
	if choice.index < 0 || choice.index >= len(choice.media) {
		return nativePlan{}, errors.New("该集没有其他可用的播放线路")
	}
	media := choice.media[choice.index]
	var err error
	if !isProviderHTTPMediaURL(media.URL) {
		return nativePlan{}, errors.New("站源未返回有效的播放地址")
	}
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nativePlan{}, errors.New("无法初始化播放会话")
	}
	plan := nativePlan{
		DanmakuID: choice.danmakuVideo,
		URL:       media.URL, Headers: map[string]string{"User-Agent": userAgent, "Referer": media.Referer},
		Key: hex.EncodeToString(media.CENCKey), Quality: media.Quality, Qualities: choice.qualities,
		RouteIndex: choice.index, RouteCount: len(choice.media), Session: hex.EncodeToString(tokenBytes),
	}
	if media.credentials != nil && !media.credentials.expires.IsZero() {
		if !time.Now().Before(media.credentials.expires) {
			return nativePlan{}, errors.New("播放凭证已过期，请重新解析播放")
		}
		plan.ExpiresAt = media.credentials.expires.UnixMilli()
	}
	choice.streamSession = ""
	{
		engine.mu.Lock()
		if engine.stream == nil {
			engine.stream, err = newNativeStreamServer(engine.downloader)
		}
		stream := engine.stream
		engine.mu.Unlock()
		if err != nil {
			return nativePlan{}, err
		}
		plan.URL, choice.streamSession = stream.nativeOpen(media)
	}
	choice.created = time.Now()
	engine.mu.Lock()
	if engine.playbacks == nil {
		engine.playbacks = map[string]nativePlaybackChoice{}
	}
	var expired []string
	for token, old := range engine.playbacks {
		if time.Since(old.created) > 12*time.Hour {
			expired = append(expired, old.streamSession)
			delete(engine.playbacks, token)
		}
	}
	if len(engine.playbacks) >= 8 {
		oldest := ""
		for token, old := range engine.playbacks {
			if oldest == "" || old.created.Before(engine.playbacks[oldest].created) {
				oldest = token
			}
		}
		expired = append(expired, engine.playbacks[oldest].streamSession)
		delete(engine.playbacks, oldest)
	}
	engine.playbacks[plan.Session] = choice
	stream := engine.stream
	engine.mu.Unlock()
	if stream != nil {
		for _, token := range expired {
			stream.nativeRelease(token)
		}
	}
	if err := ctx.Err(); err != nil {
		engine.nativeReleasePlayback(plan.Session)
		return nativePlan{}, err
	}
	return plan, nil
}

func (engine *nativeEngine) nativeNextPlayback(ctx context.Context, session string) (nativePlan, error) {
	engine.mu.Lock()
	choice, exists := engine.playbacks[session]
	engine.mu.Unlock()
	if !exists {
		return nativePlan{}, errors.New("播放线路已失效，请重新解析播放")
	}
	choice.index++
	if choice.index < len(choice.media) {
		best, bestLatency := choice.index, time.Duration(0)
		if value, ok := engine.routeLatency(nativeRouteKey(choice.media[best])); ok {
			bestLatency = value
		}
		for index := choice.index + 1; index < len(choice.media); index++ {
			value, ok := engine.routeLatency(nativeRouteKey(choice.media[index]))
			if !ok {
				continue
			}
			if bestLatency <= 0 || value < bestLatency {
				best, bestLatency = index, value
			}
		}
		choice.index = best
	}
	return engine.nativeOpenPlayback(ctx, choice)
}

func (engine *nativeEngine) nativeReleasePlayback(session string) {
	engine.mu.Lock()
	choice, exists := engine.playbacks[session]
	delete(engine.playbacks, session)
	stream := engine.stream
	engine.mu.Unlock()
	if exists && stream != nil && choice.streamSession != "" {
		stream.nativeRelease(choice.streamSession)
	}
}
