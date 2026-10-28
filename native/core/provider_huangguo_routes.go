package core

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

const huangguoVideoDomesticURL = "https://huangguoai.fit"

func sourceRouteDenied(err error) bool {
	var backoff *requestBackoff
	return errors.As(err, &backoff) && (backoff.verify || backoff.status == 401 || backoff.status == 403 || backoff.status == 429)
}

func huangguoVideoRouteHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "huangguoai.fit" || host == "huangguo.video" || host == "tw.huangguo.video" || host == "en.huangguo.video"
}

func (d *Downloader) fetchProviderTextPage(ctx context.Context, address, referer string) (string, string, error) {
	responses := &playbackResponseURLs{}
	body, err := d.fetchProviderText(context.WithValue(ctx, playbackResponseURLsKey{}, responses), address, referer)
	if actual, found := responses.values.Load(address); found {
		return body, actual.(string), err
	}
	return body, address, err
}

func (d *Downloader) huangguoVideoAssetURL(address string) string {
	u, err := url.Parse(address)
	if err != nil || !huangguoVideoRouteHost(u.Hostname()) {
		return address
	}
	candidates := d.providerURLCandidates(address)
	if len(candidates) > 0 {
		return candidates[0]
	}
	return address
}
