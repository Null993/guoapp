package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (d *Downloader) hongguoWebDetail(ctx context.Context, id string) (hongguoDetailEntry, error) {
	if !hongguoNumericID.MatchString(id) {
		return hongguoDetailEntry{}, errors.New("红果剧集 ID 无效")
	}
	client := d.hongguoClient()
	client.mu.Lock()
	if entry, ok := client.webDetails[id]; ok && time.Now().Before(entry.ExpiresAt) {
		client.mu.Unlock()
		return entry, nil
	}
	if pending := client.webPending[id]; pending != nil {
		client.mu.Unlock()
		select {
		case <-ctx.Done():
			return hongguoDetailEntry{}, ctx.Err()
		case <-pending.done:
			if (errors.Is(pending.err, context.Canceled) || errors.Is(pending.err, context.DeadlineExceeded)) && ctx.Err() == nil {
				return d.hongguoWebDetail(ctx, id)
			}
			return pending.entry, pending.err
		}
	}
	pending := &hongguoDetailCall{done: make(chan struct{})}
	client.webPending[id] = pending
	client.mu.Unlock()
	base := d.providerBaseURL(sourceHongguo)
	body, err := d.fetchProviderText(ctx, base+"/detail?series_id="+url.QueryEscape(id), base+"/")
	var entry hongguoDetailEntry
	if err == nil {
		entry, err = parseHongguoWebDetail(body, id)
	}
	client.mu.Lock()
	if err == nil {
		if len(client.webDetails) >= 128 {
			client.webDetails = map[string]hongguoDetailEntry{}
		}
		entry.ExpiresAt = time.Now().Add(5 * time.Minute)
		if !entry.countConfirmed && nativeNormalize(entry.Drama).Episodes <= 3 {
			entry.ExpiresAt = time.Now().Add(30 * time.Second)
		}
		client.webDetails[id] = entry
	}
	pending.entry, pending.err = entry, err
	delete(client.webPending, id)
	close(pending.done)
	client.mu.Unlock()
	return entry, err
}

func parseHongguoWebDetail(body, id string) (hongguoDetailEntry, error) {
	row := nestedMap(routerLoaderMap(parseRouterData(body), "detail_page", "detail_"), "seriesDetail")
	drama, err := parseHongguoSortDetail(body, id)
	if err != nil {
		return hongguoDetailEntry{}, err
	}
	entry := hongguoDetailEntry{Drama: drama}
	for _, value := range []string{mapString(row, "episode_total_cnt", "total_episode_cnt"), mapString(nestedMap(row, "series_episode_info"), "episode_total_cnt", "total_episode_cnt")} {
		if count, _ := strconv.Atoi(value); count > 0 {
			entry.countConfirmed = true
		}
	}
	seen := map[string]bool{}
	for i, value := range anyList(row["vid_list"]) {
		vid := strings.TrimSpace(fmt.Sprint(value))
		if !hongguoNumericID.MatchString(vid) || seen[vid] {
			return hongguoDetailEntry{}, errors.New("红果网页分集 ID 无效或重复")
		}
		seen[vid] = true
		entry.Chapters = append(entry.Chapters, Chapter{ID: providerChapterID(sourceHongguo, id, vid), Source: sourceHongguo, Title: fmt.Sprintf("第%d集", i+1), CurrentEpisode: rawEpisode(i + 1), VideoURL: "hongguo-cenc://" + vid})
	}
	return entry, nil
}

func (d *Downloader) hongguoPlaybackDetail(ctx context.Context, id string) (hongguoDetailEntry, error) {
	if !hongguoNumericID.MatchString(id) {
		return hongguoDetailEntry{}, errors.New("红果剧集 ID 无效")
	}
	client := d.hongguoClient()
	client.mu.Lock()
	for _, cache := range []map[string]hongguoDetailEntry{client.details, client.webDetails} {
		if entry, ok := cache[id]; ok && time.Now().Before(entry.ExpiresAt) && len(entry.Chapters) > 0 {
			client.mu.Unlock()
			return entry, nil
		}
	}
	if pending := client.playbackPending[id]; pending != nil {
		client.mu.Unlock()
		select {
		case <-ctx.Done():
			return hongguoDetailEntry{}, ctx.Err()
		case <-pending.done:
			if (errors.Is(pending.err, context.Canceled) || errors.Is(pending.err, context.DeadlineExceeded)) && ctx.Err() == nil {
				return d.hongguoPlaybackDetail(ctx, id)
			}
			return pending.entry, pending.err
		}
	}
	pending := &hongguoDetailCall{done: make(chan struct{})}
	client.playbackPending[id] = pending
	client.mu.Unlock()
	entry, err := d.raceHongguoDetails(ctx, id)
	client.mu.Lock()
	pending.entry, pending.err = entry, err
	delete(client.playbackPending, id)
	close(pending.done)
	client.mu.Unlock()
	return entry, err
}

func (d *Downloader) raceHongguoDetails(ctx context.Context, id string) (hongguoDetailEntry, error) {
	work, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	type result struct {
		entry hongguoDetailEntry
		err   error
		web   bool
	}
	results := make(chan result, 2)
	run := func(web bool) {
		go func() {
			var entry hongguoDetailEntry
			var err error
			if web {
				entry, err = d.hongguoWebDetail(work, id)
			} else {
				app, stop := context.WithTimeout(work, 12*time.Second)
				defer stop()
				entry, err = d.hongguoAppDetail(app, id)
			}
			if err == nil && len(entry.Chapters) == 0 {
				err = errors.New("未返回可播放的分集")
			}
			results <- result{entry, err, web}
		}()
	}
	run(false)
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	webStarted := false
	finished := 0
	var appErr, webErr error
	for finished < 2 {
		select {
		case <-work.Done():
			if ctx.Err() != nil {
				return hongguoDetailEntry{}, ctx.Err()
			}
			return hongguoDetailEntry{}, fmt.Errorf("红果详情请求超时，请检查网络后重试: %w", work.Err())
		case <-timer.C:
			if !webStarted {
				webStarted = true
				run(true)
			}
		case value := <-results:
			finished++
			if value.err == nil {
				return value.entry, nil
			}
			var backoff *requestBackoff
			if errors.As(value.err, &backoff) && (backoff.verify || backoff.status == 403 || backoff.status == 429) {
				return hongguoDetailEntry{}, value.err
			}
			if value.web {
				webErr = value.err
			} else {
				appErr = value.err
			}
			if !webStarted {
				webStarted = true
				run(true)
			}
		}
	}
	return hongguoDetailEntry{}, fmt.Errorf("App 详情失败: %v；网页详情失败: %w", publicError(appErr), webErr)
}
