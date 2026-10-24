package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestYeguoDiscoveryAcceptsBundledAPIAndLegacyNuxt(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			var address string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/":
					if legacy {
						fmt.Fprintf(w, `<script id="__NUXT_DATA__" type="application/json">[{"apiBaseURL":1},%q]</script>`, address+"/legacy")
					}
					io.WriteString(w, `<script type="module" src="/_nuxt/entry.js"></script>`)
				case "/_nuxt/entry.js":
					io.WriteString(w, "import { a as x } from './api.js';import { b as y } from './crypto.js';import { c as z } from 'https://other.example/_nuxt/ignored.js';")
				case "/_nuxt/api.js":
					fmt.Fprintf(w, "const endpoint=()=>`%s/api.php`;", address)
				case "/_nuxt/crypto.js":
					io.WriteString(w, "const config={version:`v0`,mode:`CBC`,padding:`Pkcs7`,key:`0123456789abcdef`,iv:`abcdef0123456789`,sign_key:`synthetic-sign`};")
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			address = server.URL
			d := sourceFixtureDownloader(t, nil)
			d.client = server.Client()
			client := &yeguoAPIClient{downloader: d, site: address}
			access, err := client.discoverConfigurationAt(context.Background(), address)
			if err != nil {
				t.Fatal(err)
			}
			want := address + "/api.php"
			if legacy {
				want = address + "/legacy"
			}
			if access.base != want || string(access.key) != "0123456789abcdef" || access.identifier == "" {
				t.Fatalf("configuration mismatch: base=%s key length=%d identifier=%t", access.base, len(access.key), access.identifier != "")
			}
		})
	}
}

func TestYeguoBundledAPIDoesNotTrustUnrelatedOrigins(t *testing.T) {
	for _, value := range []string{
		"https://unrelated.example/api.php",
		"https://www.yeguodj.com.evil.example/api.php",
		"https://user:password@www.yeguodj.com/api.php",
		"https://www.yeguodj.com/api.php?redirect=evil",
	} {
		if result := yeguoScriptAPIBase("const endpoint=`"+value+"`;", yeguoBaseURL); result != "" {
			t.Fatal("untrusted API accepted", result)
		}
	}
	if got := yeguoScriptAPIBase("const api=()=>`https://www.yeguodj.com/api.php`;", yeguoBaseURL); !strings.HasSuffix(got, "/api.php") {
		t.Fatal("public API module was not recognized")
	}
}
