package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRuntimeRuleStateChecksCurrentRuleBeforeChangingIt(t *testing.T) {
	changes := make(chan bool, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Error("missing controller authentication")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rules":
			_, _ = w.Write([]byte(`{"rules":[{"type":"Domain","payload":"example.test","proxy":"DIRECT","extra":{"disabled":false}},{"type":"Match","proxy":"DIRECT"}]}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/rules/disable":
			var body map[int]bool
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 || !body[0] {
				t.Errorf("unexpected state change: %v (%v)", body, err)
			}
			select {
			case changes <- true:
			default:
				t.Error("too many upstream changes")
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected controller request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	panel := &panelServer{control: upstream.URL, secret: "test", client: upstream.Client()}

	list := httptest.NewRecorder()
	panel.runtimeRules(list, httptest.NewRequest(http.MethodGet, "/api/rules", nil))
	var rules []runtimeRule
	if err := json.Unmarshal(list.Body.Bytes(), &rules); err != nil || list.Code != http.StatusOK || len(rules) != 2 || rules[1].Index != 1 {
		t.Fatalf("unexpected rules response: %d %s", list.Code, list.Body)
	}

	for _, test := range []struct {
		body string
		want int
	}{
		{`{"index":0,"type":"Domain","payload":"old.test","proxy":"DIRECT","disabled":true}`, http.StatusConflict},
		{`{"index":1,"type":"Match","proxy":"DIRECT","disabled":true}`, http.StatusNotImplemented},
		{`{"index":0,"type":"Domain","payload":"example.test","proxy":"DIRECT","disabled":true}`, http.StatusOK},
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/rules/state", strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		panel.setRuntimeRuleState(result, request)
		if result.Code != test.want {
			t.Fatalf("state change returned %d, expected %d", result.Code, test.want)
		}
	}
	if len(changes) != 1 {
		t.Fatal("expected exactly one change sent to Mihomo")
	}
}
