package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubscriptionUpdateKeepsLastValidDownload(t *testing.T) {
	const initial = "proxies:\n  - {name: First, type: direct}\n"
	const updated = "proxies:\n  - {name: Second, type: direct}\n"

	payload := initial
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Subscription-Userinfo", "upload=23; download=42; total=100; expire=1780000000")
		_, _ = w.Write([]byte(payload))
	}))
	defer provider.Close()

	panel := &panelServer{path: filepath.Join(t.TempDir(), "config.yaml"), subscriptions: provider.Client()}
	call := func(handler http.HandlerFunc, method, id, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/api/subscriptions/"+id, strings.NewReader(body))
		req.SetPathValue("id", id)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		handler(res, req)
		return res
	}

	res := call(panel.addSubscription, http.MethodPost, "", `{"name":"Local","content":"proxies: [{name: Local, type: direct}]"}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("local import failed: %d %s", res.Code, res.Body)
	}
	res = call(panel.addSubscription, http.MethodPost, "", `{"name":"Test","url":"`+provider.URL+`?token=hidden"}`)
	var result subscriptionInfo
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil || res.Code != http.StatusCreated ||
		result.ID == "" || result.Usage == nil || result.Usage.Total != 100 || strings.Contains(res.Body.String(), "hidden") {
		t.Fatalf("unexpected subscription response: %d %s", res.Code, res.Body)
	}
	if file, err := os.Stat(panel.path + ".subscriptions.json"); err != nil || file.Mode().Perm() != 0600 {
		t.Fatalf("subscription store permissions: %v, %v", file, err)
	}

	for _, attempt := range []struct {
		payload string
		status  int
		want    string
	}{
		{"invalid subscription response", http.StatusUnprocessableEntity, initial},
		{updated, http.StatusOK, updated},
	} {
		payload = attempt.payload
		if res := call(panel.updateSubscription, http.MethodPost, result.ID, "{}"); res.Code != attempt.status {
			t.Fatalf("update status: %d %s", res.Code, res.Body)
		}
		res := call(panel.getSubscription, http.MethodGet, result.ID, "")
		var content map[string]string
		if err := json.Unmarshal(res.Body.Bytes(), &content); err != nil || content["content"] != attempt.want || content["url"] != "" {
			t.Fatalf("unexpected saved download: %d %s", res.Code, res.Body)
		}
	}
	if res := call(panel.deleteSubscription, http.MethodDelete, result.ID, ""); res.Code != http.StatusOK {
		t.Fatalf("delete status: %d", res.Code)
	}
	if res := call(panel.updateSubscription, http.MethodPost, result.ID, "{}"); res.Code != http.StatusNotFound {
		t.Fatalf("update after delete: %d", res.Code)
	}
	res = call(panel.listSubscriptions, http.MethodGet, "", "")
	var remaining []subscriptionInfo
	if err := json.Unmarshal(res.Body.Bytes(), &remaining); err != nil || len(remaining) != 1 || remaining[0].Name != "Local" {
		t.Fatalf("unrelated subscription was changed: %d %s", res.Code, res.Body)
	}
}

func TestSubscriptionDialRequiresPublicAddress(t *testing.T) {
	for _, address := range []string{
		"127.0.0.1", "10.0.0.1", "100.100.100.200", "169.254.169.254", "192.168.1.1",
		"198.19.0.1", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "64:ff9b::a00:1",
	} {
		if publicAddress(netip.MustParseAddr(address)) {
			t.Errorf("private or reserved address allowed: %s", address)
		}
	}
	for _, address := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		if !publicAddress(netip.MustParseAddr(address)) {
			t.Errorf("public address rejected: %s", address)
		}
	}

	local := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("subscription client connected to a local address")
	}))
	defer local.Close()
	if response, err := subscriptionClient().Get(local.URL); err == nil {
		response.Body.Close()
		t.Fatal("local request should fail")
	}
}

func TestSubscriptionNegotiatesMihomoYAML(t *testing.T) {
	const content = "proxies:\n  - {name: First, type: direct}\n"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(strings.ToLower(r.UserAgent()), "clash.meta") {
			// Subscription services commonly return encoded share links to unknown clients.
			_, _ = w.Write([]byte("dmxlc3M6Ly9leGFtcGxl"))
			return
		}
		w.Header().Set("Subscription-Userinfo", "upload=1; download=2; total=100")
		_, _ = w.Write([]byte(content))
	}))
	defer provider.Close()

	panel := &panelServer{path: filepath.Join(t.TempDir(), "config.yaml"), subscriptions: provider.Client()}
	body, err := json.Marshal(map[string]string{"name": "Negotiated subscription", "url": provider.URL})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/subscriptions", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	panel.addSubscription(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("import failed: %d %s", response.Code, response.Body)
	}
	entries, err := panel.readSubscriptions()
	if err != nil || len(entries) != 1 || entries[0].Content != content || entries[0].Usage == nil {
		t.Fatalf("negotiated subscription was not saved correctly: %v", err)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/subscriptions/"+entries[0].ID, strings.NewReader("{}"))
	request.SetPathValue("id", entries[0].ID)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	panel.updateSubscription(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("update failed: %d %s", response.Code, response.Body)
	}
}
