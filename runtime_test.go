package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRuleProviderRuntimeUsesScopedControllerRoutes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("missing controller authentication")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/providers/rules":
			_, _ = w.Write([]byte(`{"providers":{"remote/example":{"updatedAt":"2026-11-24T08:00:00Z","payload":["hidden"]}}}`))
		case r.Method == http.MethodPut && r.URL.EscapedPath() == "/providers/rules/remote%2Fexample":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected controller request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	panel := &panelServer{control: upstream.URL, secret: "test-secret", client: upstream.Client()}

	list := httptest.NewRecorder()
	panel.runtimeRuleProviders(list, httptest.NewRequest(http.MethodGet, "/api/rule-providers", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "2026-11-24T08:00:00Z") ||
		strings.Contains(list.Body.String(), "hidden") {
		t.Fatalf("unexpected status response: %d %s", list.Code, list.Body)
	}

	for _, test := range []struct {
		body string
		want int
	}{
		{`{"name":"remote/example"}`, http.StatusOK},
		{`{"name":".."}`, http.StatusBadRequest},
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/rule-providers/update", strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		panel.updateRuleProvider(result, request)
		if result.Code != test.want {
			t.Fatalf("update returned %d, expected %d", result.Code, test.want)
		}
	}
}

func TestMeasureNodeDelay(t *testing.T) {
	for _, test := range []struct {
		name       string
		upstream   int
		body, want string
	}{
		{"success", http.StatusOK, `{"delay":128}`, `"delay":128`},
		{"timeout", http.StatusGatewayTimeout, `{}`, `"status":"timeout"`},
		{"unreachable", http.StatusServiceUnavailable, `{}`, `"status":"unreachable"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				if r.Method != http.MethodGet || r.URL.EscapedPath() != "/proxies/%E6%B5%8B%E9%80%9F%2Fnode/delay" ||
					query.Get("url") != "https://www.gstatic.com/generate_204" || query.Get("timeout") != "4000" || query.Get("expected") != "204" {
					t.Errorf("unexpected controller request: %s %s", r.Method, r.URL)
				}
				w.WriteHeader(test.upstream)
				_, _ = w.Write([]byte(test.body))
			}))
			defer upstream.Close()
			panel := &panelServer{control: upstream.URL, client: upstream.Client()}
			request := httptest.NewRequest(http.MethodPost, "/api/nodes/delay", strings.NewReader(`{"name":"测速/node"}`))
			request.Header.Set("Content-Type", "application/json")
			result := httptest.NewRecorder()
			panel.measureNodeDelay(result, request)
			if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), test.want) {
				t.Fatalf("unexpected delay response: %d %s", result.Code, result.Body)
			}
		})
	}
}
