package core

import (
	"context"
	"errors"
	"strings"
	"time"
)

type huangdouDetailEntry struct {
	row       map[string]any
	expiresAt time.Time
}

type huangdouDetailCall struct {
	done chan struct{}
	row  map[string]any
	err  error
}

func (d *Downloader) huangdouDetail(ctx context.Context, id string) (map[string]any, error) {
	if !rankingSourceID.MatchString(id) {
		return nil, errors.New("无效的黄豆剧集 ID")
	}
	d.providerMu.Lock()
	if cached, found := d.huangdouDetails[id]; found && time.Now().Before(cached.expiresAt) {
		d.providerMu.Unlock()
		return cached.row, nil
	}
	if pending := d.huangdouDetailPending[id]; pending != nil {
		d.providerMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pending.done:
			if (errors.Is(pending.err, context.Canceled) || errors.Is(pending.err, context.DeadlineExceeded)) && ctx.Err() == nil {
				return d.huangdouDetail(ctx, id)
			}
			return pending.row, pending.err
		}
	}
	if d.huangdouDetailPending == nil {
		d.huangdouDetailPending = make(map[string]*huangdouDetailCall)
	}
	call := &huangdouDetailCall{done: make(chan struct{})}
	d.huangdouDetailPending[id] = call
	d.providerMu.Unlock()
	var decoded any
	err := newHuangdouAPIClient(d).call(ctx, "/drama/detail", map[string]any{"id": id}, &decoded)
	row := huangdouDataMap(decoded)
	if err == nil && strings.TrimPrefix(mapString(row, "id", "drama_id"), "rp_") != id {
		row = nil
		err = errors.New("黄豆详情返回了其他剧集")
	}
	d.providerMu.Lock()
	if err == nil {
		if d.huangdouDetails == nil || len(d.huangdouDetails) >= 128 {
			d.huangdouDetails = make(map[string]huangdouDetailEntry)
		}
		d.huangdouDetails[id] = huangdouDetailEntry{row: row, expiresAt: time.Now().Add(dramaRefreshRetryDelay)}
	}
	call.row, call.err = row, err
	delete(d.huangdouDetailPending, id)
	close(call.done)
	d.providerMu.Unlock()
	return row, err
}

func (d *Downloader) huangdouMetadataRemoved(ctx context.Context, drama nativeDrama) (bool, error) {
	title := strings.TrimSpace(drama.Title)
	if title == "" || len([]rune(title)) > 512 {
		return false, nil
	}
	var decoded any
	if err := newHuangdouAPIClient(d).call(ctx, "/drama/list", map[string]any{"page": "1", "page_size": "90", "keywords": title}, &decoded); err != nil {
		return false, err
	}
	rows, valid := huangdouDataMap(decoded)["list"].([]any)
	if !valid {
		return false, errors.New("黄豆目录查询数据格式异常")
	}
	if len(rows) >= 90 {
		return false, nil
	}
	_, id, valid := splitProviderDramaID(drama.ID)
	if !valid {
		return false, nil
	}
	for _, value := range rows {
		row, valid := value.(map[string]any)
		if !valid {
			return false, errors.New("黄豆目录查询包含无效条目")
		}
		if strings.TrimPrefix(mapString(row, "id"), "rp_") == id || strings.TrimPrefix(mapString(row, "drama_id"), "rp_") == id {
			return false, nil
		}
	}
	return true, nil
}
