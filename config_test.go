package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyConfigKeepsDiskAndRuntimeOutcomesSeparate(t *testing.T) {
	const original = "rules: [MATCH,DIRECT]\n"
	const draft = "rules: [MATCH,REJECT]\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	validator := filepath.Join(dir, "mihomo")
	mustWrite := func(path, value string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), mode); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(path, original, 0600)
	mustWrite(validator, "#!/bin/sh\nif grep -q 'invalid' \"$5\"; then echo 'invalid configuration'; exit 1; fi\nexit 0\n", 0700)

	type reloadCall struct {
		path, filePath string
	}
	calls := make(chan reloadCall, 2)
	reloadStatus := make(chan int, 2)
	t.Cleanup(func() { close(reloadStatus) })
	reloadStatus <- http.StatusServiceUnavailable
	reloadStatus <- http.StatusNoContent
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.RequestURI() != "/configs?force=true" || r.Header.Get("Authorization") != "Bearer test" {
			t.Error("unexpected reload request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case calls <- reloadCall{r.URL.Path, payload["path"]}:
		default:
			t.Error("unexpected extra reload request")
		}
		select {
		case status := <-reloadStatus:
			w.WriteHeader(status)
		default:
			t.Error("unexpected reload")
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer controller.Close()

	panel := &panelServer{path: path, runtimePath: "/core/config.yaml", home: dir,
		mihomo: validator, control: controller.URL, secret: "test", client: controller.Client()}

	apply := func(content, base string) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(configInput{Content: content, Revision: base})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/apply", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		panel.applyConfig(response, request)
		return response
	}
	assertFile := func(path, expected string) {
		t.Helper()
		got, err := os.ReadFile(path)
		if err != nil || string(got) != expected {
			t.Fatalf("%s = %q, %v; want %q", path, got, err, expected)
		}
	}
	base := revision([]byte(original))
	if response := apply(draft, "stale"); response.Code != http.StatusConflict {
		t.Fatalf("stale save status = %d, want 409", response.Code)
	}
	if response := apply("invalid", base); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid save status = %d, want 422", response.Code)
	}
	assertFile(path, original)
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("invalid save created backup: %v", err)
	}

	assertResult := func(response *httptest.ResponseRecorder, applied, backup bool) {
		t.Helper()
		var result applyResult
		err := json.NewDecoder(response.Body).Decode(&result)
		if response.Code != http.StatusOK || err != nil || !result.Saved || result.Applied != applied ||
			result.Backup != backup || result.Revision != revision([]byte(draft)) {
			t.Fatalf("unexpected result: HTTP %d, %+v, %v", response.Code, result, err)
		}
	}
	assertResult(apply(draft, base), false, true)
	assertFile(path, draft)
	assertFile(path+".bak", original)
	assertResult(apply(draft, revision([]byte(draft))), true, false)
	assertFile(path+".bak", original)

	for i := 0; i < 2; i++ {
		select {
		case call := <-calls:
			if call.path != "/configs" || call.filePath != "/core/config.yaml" {
				t.Fatalf("reload targeted %+v", call)
			}
		default:
			t.Fatal("no reload request")
		}
	}
}

func TestProtectRequiresBasicAuthAndRejectsUnrecognizedLocalHost(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	for _, test := range []struct {
		password string
		host     string
		login    string
		want     int
	}{
		{"", "127.0.0.1:8787", "", 204},
		{"", "other.example", "", 403},
		{"secret", "panel.local", "", 401},
		{"secret", "panel.local", "wrong", 401},
		{"secret", "panel.local", "secret", 204},
	} {
		request := httptest.NewRequest(http.MethodGet, "/api/config", strings.NewReader(""))
		request.Host = test.host
		if test.login != "" {
			request.SetBasicAuth("admin", test.login)
		}
		response := httptest.NewRecorder()
		protect(test.password, ok).ServeHTTP(response, request)
		if response.Code != test.want {
			t.Errorf("host %q got %d, want %d", test.host, response.Code, test.want)
		}
	}
}

func TestReloadPayloadSendsFullConfig(t *testing.T) {
	const content = "mode: rule\nrules:\n  - MATCH,DIRECT\n"
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]string
		err := json.NewDecoder(r.Body).Decode(&input)
		if err != nil || r.Method != http.MethodPut || r.URL.RequestURI() != "/configs?force=true" ||
			len(input) != 1 || input["payload"] != content {
			t.Errorf("unexpected reload request: %s %s %+v %v", r.Method, r.URL.RequestURI(), input, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer controller.Close()

	panel := &panelServer{reloadPayload: true, runtimePath: "/core/config.yaml", control: controller.URL, client: controller.Client()}
	if err := panel.reload(t.Context(), content); err != nil {
		t.Fatal(err)
	}
}
