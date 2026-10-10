package core

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestSameRequestOriginMatchesSchemeHostAndPort(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		url    string
		want   bool
	}{
		{"same origin", "https://cdn.example.test", "https://cdn.example.test/cover.png", true},
		{"default https port", "https://cdn.example.test", "https://cdn.example.test:443/cover.png", true},
		{"default http port", "http://cdn.example.test:80", "http://cdn.example.test/cover.png", true},
		{"host case", "https://CDN.Example.Test", "https://cdn.example.test/cover.png", true},
		{"explicit port", "https://cdn.example.test:8443", "https://cdn.example.test:8443/cover.png", true},
		{"different port", "https://cdn.example.test", "https://cdn.example.test:8443/cover.png", false},
		{"different subdomain", "https://cdn.example.test", "https://img.cdn.example.test/cover.png", false},
		{"different scheme", "https://cdn.example.test", "http://cdn.example.test/cover.png", false},
		{"different host", "https://cdn.example.test", "https://other.example.test/cover.png", false},
		{"empty origin", "", "https://cdn.example.test/cover.png", false},
		{"unparsable origin", "https://%zz", "https://cdn.example.test/cover.png", false},
		{"origin without host", "https://", "https://cdn.example.test/cover.png", false},
	}
	for _, item := range cases {
		request, err := http.NewRequest(http.MethodGet, item.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := sameRequestOrigin(item.origin, request); got != item.want {
			t.Fatalf("%s: sameRequestOrigin(%q, %q) = %v, want %v", item.name, item.origin, item.url, got, item.want)
		}
	}
}

func TestAccessForRequestRequiresSameOrigin(t *testing.T) {
	previousBuild := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = previousBuild }()

	downloader := sourceFixtureDownloader(t, nil)
	if _, err := downloader.saveSourceAccess(nativeInput{
		Source: sourceHuangdou, Origin: "https://tideember.cc",
		Cookie: "session=owned", UserAgent: "OwnUA/1.0", AccessToken: "token-owned", DeviceID: "device-owned",
	}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"same origin", "https://tideember.cc/api/drama/list", true},
		{"other mirror host", "https://xqjurgek.top/api/drama/list", false},
		{"different port", "https://tideember.cc:8443/api/drama/list", false},
		{"different scheme", "http://tideember.cc/api/drama/list", false},
		{"subdomain", "https://cdn.tideember.cc/api/drama/list", false},
		{"unrelated host", "https://example.test/api/drama/list", false},
	}
	for _, item := range cases {
		request, err := http.NewRequest(http.MethodGet, item.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		access, found := downloader.accessForRequest(request)
		if found != item.want {
			t.Fatalf("%s: accessForRequest(%q) found = %v, want %v", item.name, item.url, found, item.want)
		}
		if found && (access.Cookie != "session=owned" || access.Token != "token-owned" || access.Origin != "https://tideember.cc") {
			t.Fatalf("%s: 同源请求应携带本人凭据：%+v", item.name, access)
		}
	}
}

func crossOriginRedirectEngine(t *testing.T, status int, location string, seen *[]*http.Request, mu *sync.Mutex) *nativeEngine {
	t.Helper()
	return sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		*seen = append(*seen, request.Clone(request.Context()))
		mu.Unlock()
		if request.URL.Path == "/api/start" {
			response := sourceFixtureResponse(request, status, "")
			response.Header.Set("Location", location)
			return response, nil
		}
		return sourceFixtureResponse(request, http.StatusOK, "ok"), nil
	})
}

func TestHuangdouPostCrossOriginRedirectIsRejected(t *testing.T) {
	var mu sync.Mutex
	var seen []*http.Request
	engine := crossOriginRedirectEngine(t, http.StatusTemporaryRedirect, "https://other.example.test/api/landing", &seen, &mu)
	request, err := http.NewRequest(http.MethodPost, "https://tideember.cc/api/start", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Cookie", "session=owned")
	request.Header.Set("Authorization", "Bearer owned")
	if _, err := engine.downloader.client.Do(request); err == nil || !strings.Contains(err.Error(), "黄豆授权请求不能跨域重定向") {
		t.Fatalf("跨域 307 授权请求应硬失败：%v", err)
	}
}

func TestHuangdouCrossOriginGetRedirectDropsCredentials(t *testing.T) {
	var mu sync.Mutex
	var seen []*http.Request
	engine := crossOriginRedirectEngine(t, http.StatusFound, "https://other.example.test/api/landing", &seen, &mu)
	request, err := http.NewRequest(http.MethodPost, "https://tideember.cc/api/start", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Cookie", "session=owned")
	request.Header.Set("Authorization", "Bearer owned")
	request.Header.Set("X-Preview-Token", "preview-owned")
	if _, err := engine.downloader.client.Do(request); err != nil {
		t.Fatalf("302 跨域重定向不应硬失败：%v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("重定向链请求数 = %d，期望 2", len(seen))
	}
	redirected := seen[1]
	if redirected.Header.Get("Cookie") != "" || redirected.Header.Get("Authorization") != "" || redirected.Header.Get("X-Preview-Token") != "" {
		t.Fatalf("跨域重定向不应附带原域凭据：%+v", redirected.Header)
	}
}

func TestHuangdouSameOriginRedirectKeepsCredentials(t *testing.T) {
	var mu sync.Mutex
	var seen []*http.Request
	engine := crossOriginRedirectEngine(t, http.StatusTemporaryRedirect, "https://tideember.cc/api/landing", &seen, &mu)
	request, err := http.NewRequest(http.MethodPost, "https://tideember.cc/api/start", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Cookie", "session=owned")
	if _, err := engine.downloader.client.Do(request); err != nil {
		t.Fatalf("同源重定向不应失败：%v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[1].Header.Get("Cookie") != "session=owned" {
		t.Fatalf("同源重定向应保留凭据：%+v", seen)
	}
}

func TestHuangdouCredentialsOnlyAttachOnSameOrigin(t *testing.T) {
	previousBuild := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = previousBuild }()

	cases := []struct {
		name   string
		host   string
		attach bool
	}{
		{"same origin", "https://tideember.cc", true},
		{"other mirror origin", "https://xqjurgek.top", false},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			var mu sync.Mutex
			var captured *http.Request
			var body []byte
			downloader := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
				mu.Lock()
				captured = request.Clone(request.Context())
				if request.Body != nil {
					body, _ = io.ReadAll(request.Body)
				}
				mu.Unlock()
				return sourceFixtureResponse(request, http.StatusOK, ""), nil
			})
			if _, err := downloader.saveSourceAccess(nativeInput{
				Source: sourceHuangdou, Origin: "https://tideember.cc",
				Cookie: "session=owned", UserAgent: "OwnUA/1.0", AccessToken: "token-owned", DeviceID: "device-owned",
			}); err != nil {
				t.Fatal(err)
			}
			client := &huangdouAPIClient{d: downloader, host: item.host, sessionID: "fixture-session", deviceID: "fixture-device"}
			_ = client.callOnce(context.Background(), "/drama/list", map[string]any{"page": "1"}, nil)

			mu.Lock()
			request, payload := captured, append([]byte{}, body...)
			mu.Unlock()
			if request == nil {
				t.Fatal("未捕获到黄豆请求")
			}
			key, err := huangdouKey(request.Header.Get("requestId"))
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := huangdouDecode(payload, key)
			if err != nil {
				t.Fatalf("请求体应可解码：%v", err)
			}
			fields, _ := decoded.(map[string]any)
			token := mapString(fields, "token")
			if item.attach {
				if request.Header.Get("Cookie") != "session=owned" || request.Header.Get("User-Agent") != "OwnUA/1.0" || token != "token-owned" {
					t.Fatalf("同源请求应附带本人凭据：cookie=%q ua=%q token=%q", request.Header.Get("Cookie"), request.Header.Get("User-Agent"), token)
				}
				return
			}
			if request.Header.Get("Cookie") != "" || token != "" || request.Header.Get("User-Agent") == "OwnUA/1.0" {
				t.Fatalf("跨域请求不应附带本人凭据：cookie=%q ua=%q token=%q", request.Header.Get("Cookie"), request.Header.Get("User-Agent"), token)
			}
		})
	}
}
