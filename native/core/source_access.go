package core

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

type sourceAuthorizationError struct {
	message string
}

func (failure *sourceAuthorizationError) Error() string { return failure.message }

func (d *Downloader) sourceAccessStatus(source string) map[string]any {
	source = canonicalProviderSource(source)
	d.sourceAccessMu.RLock()
	access, configured := d.sourceAccess[source]
	d.sourceAccessMu.RUnlock()
	if access.Origin == "" && source == sourceSorani {
		address, _ := url.Parse(d.soraniClient().base)
		access.Origin = providerMediaOrigin(address)
	}
	return map[string]any{
		"source":     source,
		"url":        d.providerBaseURL(source),
		"configured": configured,
		"origin":     access.Origin,
	}
}

func (d *Downloader) saveSourceAccess(input nativeInput) (map[string]any, error) {
	source := canonicalProviderSource(input.Source)
	if !nativeSourceAvailable(source) {
		return nil, errNativeBuildSource
	}
	origin := strings.TrimRight(strings.TrimSpace(input.Origin), "/")
	if origin == "" {
		origin = d.providerBaseURL(source)
		if source == sourceSorani {
			address, _ := url.Parse(d.soraniClient().base)
			origin = providerMediaOrigin(address)
		}
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return nil, errors.New("验证地址必须是该站点的 HTTPS 域名")
	}
	if providerSourceForURL(origin+"/") != source {
		configured, _ := url.Parse(d.providerBaseURL(source))
		if configured == nil || !strings.EqualFold(configured.Host, parsed.Host) {
			return nil, errors.New("验证地址与当前站源不匹配")
		}
	}
	access := nativeSourceAccess{
		Source:    source,
		Origin:    origin,
		Cookie:    strings.TrimSpace(input.Cookie),
		UserAgent: strings.TrimSpace(input.UserAgent),
		Token:     strings.TrimSpace(input.AccessToken),
		DeviceID:  strings.TrimSpace(input.DeviceID),
	}
	if source == sourceSorani {
		api, _ := url.Parse(d.soraniClient().base)
		if providerMediaOrigin(api) != providerMediaOrigin(parsed) {
			return nil, errors.New("青空登录会话应绑定接口 HTTPS 域名，请保留默认验证地址")
		}
		if strings.HasPrefix(strings.ToLower(access.Token), "bearer ") {
			access.Token = strings.TrimSpace(access.Token[7:])
		}
		if access.DeviceID != "" && (len(access.DeviceID) < 8 || len(access.DeviceID) > 64 || strings.Trim(access.DeviceID, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "") {
			return nil, errors.New("青空访客标识格式无效")
		}
	}
	if len(access.Cookie) > 8192 || len(access.UserAgent) > 512 || len(access.Token) > 4096 || len(access.DeviceID) > 128 ||
		strings.ContainsAny(access.Cookie+access.UserAgent+access.Token+access.DeviceID, "\r\n") {
		return nil, errors.New("站点会话内容无效或过长")
	}
	if access.Token != "" && source != sourceHuangdou && source != sourceSorani {
		return nil, errors.New("当前站源不支持令牌导入")
	}
	if access.Cookie == "" && access.Token == "" {
		return nil, errors.New("需要浏览器 Cookie 或本人站点授权令牌")
	}
	d.sourceAccessMu.Lock()
	if d.sourceAccess == nil {
		d.sourceAccess = map[string]nativeSourceAccess{}
	}
	d.sourceAccess[source] = access
	d.sourceAccessMu.Unlock()
	if d.limiter != nil {
		d.limiter.mu.Lock()
		delete(d.limiter.backoffs, strings.ToLower(parsed.Hostname()))
		delete(d.limiter.backgroundBackoffs, strings.ToLower(parsed.Hostname()))
		d.limiter.notifyLocked()
		d.limiter.mu.Unlock()
	}
	return d.sourceAccessStatus(source), nil
}

func (d *Downloader) clearSourceAccess(source string) {
	source = canonicalProviderSource(source)
	d.sourceAccessMu.Lock()
	delete(d.sourceAccess, source)
	d.sourceAccessMu.Unlock()
}

func (d *Downloader) clearAllSourceAccess() {
	d.sourceAccessMu.Lock()
	d.sourceAccess = nil
	d.sourceAccessMu.Unlock()
}

func (d *Downloader) sourceAccessFor(source string) (nativeSourceAccess, bool) {
	d.sourceAccessMu.RLock()
	defer d.sourceAccessMu.RUnlock()
	access, found := d.sourceAccess[canonicalProviderSource(source)]
	return access, found
}

func (d *Downloader) accessForRequest(request *http.Request) (nativeSourceAccess, bool) {
	d.sourceAccessMu.RLock()
	defer d.sourceAccessMu.RUnlock()
	for _, access := range d.sourceAccess {
		if sameRequestOrigin(access.Origin, request) {
			return access, true
		}
	}
	return nativeSourceAccess{}, false
}

func (d *Downloader) applySourceRequestAccess(request *http.Request) {
	access, configured := d.accessForRequest(request)
	if !configured {
		return
	}
	if access.Cookie != "" {
		request.Header.Set("Cookie", access.Cookie)
	}
	if access.UserAgent != "" {
		request.Header.Set("User-Agent", access.UserAgent)
	}
	if access.Source == sourceSorani {
		if access.Token != "" {
			request.Header.Set("Authorization", "Bearer "+access.Token)
		}
		if access.DeviceID != "" {
			request.Header.Set("X-Sorani-Guest-Key", access.DeviceID)
		}
	}
}

func sameRequestOrigin(origin string, request *http.Request) bool {
	configured, err := url.Parse(origin)
	if err != nil || configured.Hostname() == "" || !strings.EqualFold(configured.Scheme, request.URL.Scheme) {
		return false
	}
	if !strings.EqualFold(configured.Hostname(), request.URL.Hostname()) {
		return false
	}
	return normalizedOriginPort(configured) == normalizedOriginPort(request.URL)
}

func normalizedOriginPort(address *url.URL) string {
	port := address.Port()
	if port == "" {
		return ""
	}
	if port == "443" && strings.EqualFold(address.Scheme, "https") {
		return ""
	}
	if port == "80" && strings.EqualFold(address.Scheme, "http") {
		return ""
	}
	return port
}
