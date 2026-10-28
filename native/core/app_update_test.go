package core

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func updateFixtureRelease(tag, slug, platform string) appRelease {
	v, _ := parseAppReleaseVersion(tag)
	name := slug + "-" + appVersionText(v) + "-" + platform
	return appRelease{Tag: tag, URL: "https://github.com/" + updateRepository + "/releases/tag/" + tag, Assets: []appReleaseAsset{{Name: name, URL: "https://github.com/" + updateRepository + "/releases/download/" + tag + "/" + name, Size: 5, Digest: "sha256:" + strings.Repeat("a", 64)}}}
}

func TestAppUpdateVersionAndPlatformSelection(t *testing.T) {
	latest := updateFixtureRelease("app-v0.2.83-90", "zhenguojian", "windows-x64.zip")
	cases := []struct {
		name     string
		releases []appRelease
		platform string
		abis     []string
		all      bool
		status   string
	}{
		{"new windows", []appRelease{latest}, "windows", nil, true, "available"},
		{"different edition", []appRelease{latest}, "windows", nil, false, "no_asset"},
		{"wrong platform", []appRelease{latest}, "android", []string{"arm64-v8a"}, true, "no_asset"},
		{"local newer", []appRelease{updateFixtureRelease("app-v0.2.68-75", "zhenguojian", "arm64-v8a.apk")}, "windows", nil, true, "local_newer"},
		{"equal", []appRelease{updateFixtureRelease("v0.2.82+89", "zhenguojian", "windows-x64.zip")}, "windows", nil, true, "current"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := selectAppRelease(tc.releases, tc.platform, tc.abis, tc.all)
			if err != nil || result.Status != tc.status {
				t.Fatal(result, err)
			}
		})
	}
	android := updateFixtureRelease("0.2.83+90", "zhenguojian", "armeabi-v7a.apk")
	arm64 := updateFixtureRelease("0.2.83+90", "zhenguojian", "arm64-v8a.apk")
	android.Assets = append(android.Assets, arm64.Assets...)
	result, err := selectAppRelease([]appRelease{android}, "android", []string{"arm64-v8a", "armeabi-v7a"}, true)
	if err != nil || result.Asset == nil || !strings.HasSuffix(result.Asset.Name, "arm64-v8a.apk") {
		t.Fatal("ABI preference not respected", result, err)
	}
	latest.Draft = true
	if _, err := selectAppRelease([]appRelease{latest}, "windows", nil, true); err == nil {
		t.Fatal("draft selected")
	}
	latest.Draft = false
	latest.Prerelease = true
	if _, err := selectAppRelease([]appRelease{latest}, "windows", nil, true); err == nil {
		t.Fatal("prerelease selected")
	}
	for _, bad := range []string{"1.2", "1.2.3-beta", "app-v1.2.3-4-extra", "99999999999999999999.1.1"} {
		if _, ok := parseAppReleaseVersion(bad); ok {
			t.Fatal("invalid version accepted", bad)
		}
	}
}

func TestAppUpdateDownloadVerifiesChecksumAndDoesNotExposePartial(t *testing.T) {
	for _, scenario := range []string{"success", "mismatch", "truncated", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			payload := []byte("fixture package")
			sum := sha256.Sum256(payload)
			checksum := hex.EncodeToString(sum[:])
			d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "SHA256SUMS.txt") {
					return sourceFixtureResponse(r, 200, checksum+"  fixture.zip\n"), nil
				}
				if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
					t.Fatal("credentials leaked")
				}
				if scenario == "truncated" {
					return sourceFixtureResponse(r, 200, "partial"), nil
				}
				if scenario == "cancelled" {
					return nil, context.Canceled
				}
				return sourceFixtureResponse(r, 200, string(payload)), nil
			})
			e := &nativeEngine{directory: t.TempDir(), downloader: d}
			asset := appReleaseAsset{Name: "fixture.zip", URL: "https://github.com/" + updateRepository + "/releases/download/v1/fixture.zip", Size: int64(len(payload))}
			sums := appReleaseAsset{Name: "SHA256SUMS.txt", URL: "https://github.com/" + updateRepository + "/releases/download/v1/SHA256SUMS.txt", Size: 100}
			state := nativeUpdateState{Asset: &asset, checksum: &sums}
			if scenario == "mismatch" {
				state.SHA256 = strings.Repeat("b", 64)
			}
			path, _, err := e.fetchAppUpdate(context.Background(), "fixture-job", state)
			if scenario == "success" {
				if err != nil {
					t.Fatal(err)
				}
				body, _ := os.ReadFile(path)
				if string(body) != string(payload) {
					t.Fatal("bad package")
				}
			} else if err == nil {
				t.Fatal("bad download accepted", scenario)
			}
			matches, _ := filepath.Glob(filepath.Join(e.directory, "updates", "fixture-job", "*.part"))
			if len(matches) != 0 {
				t.Fatal("partial left behind")
			}
			if scenario != "success" {
				if _, err := os.Stat(filepath.Join(e.directory, "updates", "fixture-job", "fixture.zip")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("failed package exposed")
				}
			}
		})
	}
}

func TestAppUpdateCacheAndRateLimit(t *testing.T) {
	release := updateFixtureRelease("app-v0.2.83-90", "zhenguojian", "windows-x64.zip")
	if runtime.GOOS == "android" {
		release = updateFixtureRelease("app-v0.2.83-90", "zhenguojian", "x86_64.apk")
	}
	body, _ := json.Marshal([]appRelease{release})
	calls := 0
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		calls++
		switch calls {
		case 1:
			response := sourceFixtureResponse(r, 200, string(body))
			response.Header.Set("ETag", `"fixture"`)
			return response, nil
		case 2:
			if r.Header.Get("If-None-Match") != `"fixture"` {
				t.Fatal("missing cache validator")
			}
			return sourceFixtureResponse(r, 304, ""), nil
		default:
			return sourceFixtureResponse(r, 403, ""), nil
		}
	})
	e := &nativeEngine{directory: t.TempDir(), downloader: d}
	input := nativeUpdateInput{Platform: runtime.GOOS, ABIs: []string{"x86_64"}}
	for range 2 {
		result, err := e.checkAppUpdate(context.Background(), input)
		if err != nil || result.Version != "0.2.83+90" {
			t.Fatal(result, err)
		}
	}
	if _, err := e.checkAppUpdate(context.Background(), input); err == nil {
		t.Fatal("rate limit incorrectly treated as current")
	}
}

func TestAppUpdateArchiveRejectsUnsafeAndIncompletePackages(t *testing.T) {
	for _, bad := range []string{"../escape.dll", "C:/escape.dll", "data/../../escape.dll", "data/evil:stream", "data/file.", "data/File.dll", "data/CON.dll"} {
		t.Run(bad, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "fixture.zip")
			f, _ := os.Create(path)
			archive := zip.NewWriter(f)
			for _, name := range []string{"zhenguojian.exe", "flutter_windows.dll", "duanju_core.dll", "data/app.so", "data/icudtl.dat", "data/file.dll", bad} {
				entry, err := archive.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				entry.Write([]byte("fixture"))
			}
			archive.Close()
			f.Close()
			if _, err := unpackAppUpdate(path, filepath.Join(directory, "stage"), "zhenguojian.exe"); err == nil {
				t.Fatal("unsafe archive accepted", bad)
			}
		})
	}
	if _, err := parseUpdateChecksums([]byte(strings.Repeat("a", 64)+"  other.apk"), "wanted.apk"); err == nil {
		t.Fatal("wrong checksum filename accepted")
	}
	for _, address := range []string{"http://github.com/a", "https://github.com.evil.test/a", "https://user@github.com/a", "file:///tmp/a", "https://github.com:444/a"} {
		if validUpdateURL(address) {
			t.Fatal("unsafe URL accepted", address)
		}
	}
}
