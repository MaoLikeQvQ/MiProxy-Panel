package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRuntimeOverview(t *testing.T) {
	for _, test := range []struct {
		name, connections, memory string
		active                    int
		online                    bool
	}{
		{"active connections", `{"downloadTotal":157,"uploadTotal":85,"connections":[{"host":"private.test"}]}`, "{\"inuse\":0}\n{\"inuse\":16148070}\n", 1, true},
		{"no connections", `{"downloadTotal":0,"uploadTotal":0,"connections":null}`, `{"inuse":16148070}`, 0, true},
		{"partial snapshot", `{}`, `{"inuse":0}`, 0, true},
		{"controller offline", ``, ``, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-secret" {
					t.Error("missing controller authentication")
				}
				switch r.URL.Path {
				case "/version":
					if !test.online {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					_, _ = io.WriteString(w, `{"version":"v1.19.34"}`)
				case "/connections":
					_, _ = io.WriteString(w, test.connections)
				case "/memory":
					_, _ = io.WriteString(w, test.memory)
				default:
					t.Errorf("unexpected controller request: %s", r.URL)
				}
			}))
			defer upstream.Close()
			panel := &panelServer{control: upstream.URL, secret: "test-secret", client: upstream.Client()}
			result := httptest.NewRecorder()
			panel.runtimeOverview(result, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
			body := result.Body.String()
			var overview runtimeOverview
			if err := json.Unmarshal([]byte(body), &overview); err != nil || result.Code != http.StatusOK {
				t.Fatalf("unexpected response: %d %s", result.Code, body)
			}
			if overview.Connected != test.online || overview.Controller != upstream.URL ||
				(overview.Version == "v1.19.34") != test.online || strings.Contains(body, "private.test") || strings.Contains(body, "test-secret") {
				t.Fatalf("unexpected controller status: %s", body)
			}
			if test.name == "partial snapshot" || !test.online {
				if overview.Counters != nil || overview.Memory != nil {
					t.Fatalf("unexpected metrics: %s", body)
				}
				return
			}
			if overview.Counters == nil || overview.Counters.Active != test.active || overview.Counters.SampledAt <= 0 ||
				overview.Memory == nil || *overview.Memory != 16148070 {
				t.Fatalf("unexpected metrics: %s", body)
			}
			if test.active > 0 && (overview.Counters.Downloaded != 157 || overview.Counters.Uploaded != 85) {
				t.Fatalf("unexpected traffic totals: %s", body)
			}
		})
	}
}
