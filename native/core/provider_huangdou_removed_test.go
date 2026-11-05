package core

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestHuangdouMetadataRejectNeedsCatalogConfirmation(t *testing.T) {
	for _, fixture := range []struct {
		name, code, list string
		status           int
		missing          bool
		probes           int
	}{
		{"removed", "2001", `[]`, 200, true, 1},
		{"still-listed", "2001", `[{"id":"123","name":"合成旧条目"}]`, 200, false, 1},
		{"replacement-id", "2001", `[{"id":"456","name":"合成旧条目"}]`, 200, true, 1},
		{"explicit-reference", "2001", `[{"id":"456","drama_id":"rp_123","name":"合成旧条目"}]`, 200, false, 1},
		{"authorization", "813005", `[]`, 200, false, 0},
		{"malformed-list", "2001", `{}`, 200, false, 1},
		{"network-failure", "2001", `[]`, 503, false, 1},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			probes := 0
			engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == "/api/drama/detail" {
					return sourceFixtureResponse(request, 200, fmt.Sprintf(`{"status":"n","errorCode":%q,"msg":"请求数据错误"}`, fixture.code)), nil
				}
				if request.URL.Path != "/api/drama/list" {
					t.Fatal(request.URL.Path)
				}
				probes++
				return sourceFixtureResponse(request, fixture.status, fmt.Sprintf(`{"status":"y","data":{"list":%s}}`, fixture.list)), nil
			})
			item := engine.fetchSourceMetadata(context.Background(), nativeDrama{ID: "huangdou:123", Source: sourceHuangdou, Title: "合成旧条目"}, 0)
			if item.err == nil || sourceMetadataMissing(item.err) != fixture.missing || probes != fixture.probes {
				t.Fatal(item.err, probes)
			}
			if fixture.name == "still-listed" || fixture.name == "explicit-reference" {
				if _, temporary := engine.sourceRetryDelay(item.err); !temporary {
					t.Fatal("listed metadata rejection did not receive automatic retry", item.err)
				}
			}
		})
	}
}
