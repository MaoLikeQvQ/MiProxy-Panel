package access

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) *Manager {
	t.Helper()
	manager, err := Open(filepath.Join(t.TempDir(), "panel.access.json"))
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func localRequest(method, body string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1:8787/api/settings/access", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:40123"
	r.Header.Set("Content-Type", "application/json")
	return r
}

func update(t *testing.T, manager *Manager, password, current string) *httptest.ResponseRecorder {
	t.Helper()
	content, _ := json.Marshal(map[string]string{"password": password})
	r := localRequest(http.MethodPost, string(content))
	if current != "" {
		r.SetBasicAuth("admin", current)
	}
	response := httptest.NewRecorder()
	manager.Handler(http.HandlerFunc(manager.UpdateSettings)).ServeHTTP(response, r)
	return response
}

func TestFreshManagerOnlyAllowsRealLocalPeersAndHosts(t *testing.T) {
	manager := fixture(t)
	for _, test := range []struct {
		name, peer, host string
		allowed          bool
	}{
		{"IPv4", "127.0.0.1:40123", "127.0.0.1:8787", true},
		{"IPv6", "[::1]:40123", "[::1]:8787", true},
		{"localhost", "127.0.0.1:40123", "localhost:8787", true},
		{"remote spoofed Host", "192.168.1.5:40123", "localhost:8787", false},
		{"remote LAN", "192.168.1.5:40123", "192.168.1.1:8787", false},
		{"DNS rebinding", "127.0.0.1:40123", "example.com:8787", false},
		{"no peer", "", "localhost:8787", false},
		{"fake localhost", "127.0.0.1:40123", "localhost.example.com", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := localRequest(http.MethodGet, "")
			r.RemoteAddr, r.Host = test.peer, test.host
			response := httptest.NewRecorder()
			manager.Handler(http.HandlerFunc(manager.GetSettings)).ServeHTTP(response, r)
			want := http.StatusForbidden
			if test.allowed {
				want = http.StatusOK
			}
			if response.Code != want {
				t.Fatalf("status %d, want %d: %s", response.Code, want, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Frame-Options") != "DENY" {
				t.Fatal("missing security headers")
			}
		})
	}
}

func TestSetPasswordPersistsHashAndAuthenticatesAfterRestart(t *testing.T) {
	manager := fixture(t)
	password := "correct horse battery"
	if response := update(t, manager, password, ""); response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	content, err := os.ReadFile(manager.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), password) || !strings.Contains(string(content), `"hash"`) || strings.Contains(string(content), `"password"`) {
		t.Fatalf("password was not stored as a hash: %s", content)
	}
	info, _ := os.Stat(manager.path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("credentials mode = %o", info.Mode().Perm())
	}
	restored, err := Open(manager.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		username, password string
		allowed            bool
	}{
		{"", "", false}, {"admin", "wrong-password", false}, {"other", password, false}, {"admin", password, true},
	} {
		r := localRequest(http.MethodGet, "")
		r.RemoteAddr, r.Host = "192.168.1.5:40123", "192.168.1.1:8787"
		if test.username != "" {
			r.SetBasicAuth(test.username, test.password)
		}
		response := httptest.NewRecorder()
		restored.Handler(http.HandlerFunc(restored.GetSettings)).ServeHTTP(response, r)
		if test.allowed {
			if response.Code != http.StatusOK {
				t.Fatal(response.Code, response.Body.String())
			}
			var settings Settings
			if json.Unmarshal(response.Body.Bytes(), &settings) != nil || !settings.PasswordSet || settings.LocalOnly || settings.Username != "admin" {
				t.Fatalf("invalid public settings: %s", response.Body.String())
			}
			if strings.Contains(response.Body.String(), "hash") || strings.Contains(response.Body.String(), "salt") {
				t.Fatal("credential material exposed")
			}
		} else if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("expected authentication challenge, got %d", response.Code)
		}
	}
}

func TestPasswordChangesInvalidateOldPasswordAndClearRestoresLocalOnly(t *testing.T) {
	manager := fixture(t)
	if update(t, manager, "first-password", "").Code != http.StatusOK || update(t, manager, "second-password", "first-password").Code != http.StatusOK {
		t.Fatal("could not set passwords")
	}
	if update(t, manager, "third-password", "first-password").Code != http.StatusUnauthorized {
		t.Fatal("old password still accepted")
	}
	if update(t, manager, "", "second-password").Code != http.StatusOK {
		t.Fatal("could not clear password")
	}
	restored, err := Open(manager.path)
	if err != nil {
		t.Fatal(err)
	}
	r := localRequest(http.MethodGet, "")
	response := httptest.NewRecorder()
	restored.Handler(http.HandlerFunc(restored.GetSettings)).ServeHTTP(response, r)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"localOnly":true`) {
		t.Fatal(response.Code, response.Body.String())
	}
	r.RemoteAddr, r.Host = "192.168.1.5:1234", "192.168.1.1:8787"
	r.SetBasicAuth("admin", "second-password")
	response = httptest.NewRecorder()
	restored.Handler(http.HandlerFunc(restored.GetSettings)).ServeHTTP(response, r)
	if response.Code != http.StatusForbidden {
		t.Fatal("cleared credentials still allow remote requests")
	}
}

func TestSocketBootstrapDoesNotTrustSpoofedHeaders(t *testing.T) {
	manager := fixture(t)
	r := localRequest(http.MethodPost, `{"password":"socket-password"}`)
	r.RemoteAddr, r.Host = "@", "localhost"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	response := httptest.NewRecorder()
	manager.UpdateSettings(response, r)
	if response.Code != http.StatusForbidden {
		t.Fatal("direct handler accepted untrusted request")
	}
	response = httptest.NewRecorder()
	manager.LocalHandler(http.HandlerFunc(manager.UpdateSettings)).ServeHTTP(response, r)
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	// A privileged Unix client remains usable after Web credentials are set.
	r = localRequest(http.MethodGet, "")
	r.RemoteAddr = "@"
	response = httptest.NewRecorder()
	manager.LocalHandler(http.HandlerFunc(manager.GetSettings)).ServeHTTP(response, r)
	if response.Code != http.StatusOK {
		t.Fatal("Web password blocked permission-protected Unix control")
	}
}

func TestInvalidStoredCredentialsFailClosed(t *testing.T) {
	for _, content := range []string{"", "{}", "null", `{"version":2}`, `{"version":1,"hash":"broken"}`, `{"version":1} {}`, `{"version":1,"unexpected":true}`} {
		t.Run(content, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "access.json")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(path); err == nil {
				t.Fatal("invalid file silently disabled authentication")
			}
		})
	}
	t.Run("permissions", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "access.json")
		os.WriteFile(path, []byte(`{"version":1}`), 0644)
		if _, err := Open(path); err == nil {
			t.Fatal("public credential file accepted")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		os.WriteFile(target, []byte(`{"version":1}`), 0600)
		link := filepath.Join(dir, "access.json")
		os.Symlink(target, link)
		if _, err := Open(link); err == nil {
			t.Fatal("credential symlink accepted")
		}
	})
}

func TestWriteFailurePreservesCurrentPolicy(t *testing.T) {
	manager := fixture(t)
	if update(t, manager, "first-password", "").Code != http.StatusOK {
		t.Fatal("initial save failed")
	}
	original, _ := os.ReadFile(manager.path)
	manager.path = filepath.Join(t.TempDir(), "missing", "access.json")
	if response := update(t, manager, "new-password", "first-password"); response.Code != http.StatusInternalServerError {
		t.Fatal(response.Code, response.Body.String())
	}
	if permitted(localRequest(http.MethodGet, ""), manager.data) {
		t.Fatal("failed write unexpectedly removed password protection")
	}
	r := localRequest(http.MethodGet, "")
	r.SetBasicAuth("admin", "first-password")
	if !permitted(r, manager.data) {
		t.Fatal("failed write changed the active password")
	}
	persisted, _ := json.Marshal(manager.data)
	if strings.TrimSpace(string(original)) != string(persisted) {
		t.Fatal("failed save changed credential state")
	}
}

func TestUpdateRejectsInvalidRequestsAndLeavesFileAbsent(t *testing.T) {
	for _, test := range []struct {
		name, contentType, body string
		want                    int
	}{
		{"form", "application/x-www-form-urlencoded", "password=long-password", http.StatusUnsupportedMediaType},
		{"missing password", "application/json", `{}`, http.StatusBadRequest},
		{"null password", "application/json", `{"password":null}`, http.StatusBadRequest},
		{"short password", "application/json", `{"password":"short"}`, http.StatusBadRequest},
		{"unknown property", "application/json", `{"password":"long-password","other":true}`, http.StatusBadRequest},
		{"trailing object", "application/json", `{"password":"long-password"} {}`, http.StatusBadRequest},
		{"oversize password", "application/json", `{"password":"` + strings.Repeat("x", 1025) + `"}`, http.StatusBadRequest},
		{"oversize body", "application/json", `{"password":"` + strings.Repeat("x", 9000) + `"}`, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := fixture(t)
			r := localRequest(http.MethodPost, test.body)
			r.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			manager.UpdateSettings(response, r)
			if response.Code != test.want {
				t.Fatalf("status %d, want %d: %s", response.Code, test.want, response.Body.String())
			}
			if _, err := os.Stat(manager.path); !os.IsNotExist(err) {
				t.Fatal("invalid input wrote credentials")
			}
		})
	}
}
