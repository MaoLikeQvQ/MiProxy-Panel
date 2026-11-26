package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubscriptionImportApplyDeleteThroughLocalAPI(t *testing.T) {
	p := testCorePanel(t)
	validator := filepath.Join(p.home, "validator")
	if err := os.WriteFile(validator, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	p.mihomo = validator
	const initial = "mixed-port: 17996\ndns: {enable: true}\nrules: [\"MATCH,DIRECT\"]\n"
	if err := os.WriteFile(p.path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	reloadOK := true
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/configs" {
			http.NotFound(w, r)
			return
		}
		if reloadOK {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer controller.Close()
	p.control = controller.URL
	p.client = controller.Client()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/subscriptions", p.addSubscription)
	mux.HandleFunc("GET /api/subscriptions", p.listSubscriptions)
	mux.HandleFunc("DELETE /api/subscriptions/{id}", p.deleteSubscription)
	mux.HandleFunc("POST /api/subscriptions/{id}/apply", p.applySubscription)
	path := localSocketTestPath(t)
	closeLocal, err := startLocalControl(mux, path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeLocal()
	client := newSubscriptionTestClient(path)
	defer client.http.CloseIdleConnections()
	ctx := context.Background()
	var first, second subscriptionInfo
	for i, entry := range []*subscriptionInfo{&first, &second} {
		name := []string{"订阅甲", "订阅乙"}[i]
		err = client.request(ctx, "POST", "/api/subscriptions", map[string]string{"name": name, "content": "proxies: [{name: " + name + ", type: direct}]\nrules: [\"MATCH," + name + "\"]\n"}, entry)
		if err != nil {
			t.Fatal(err)
		}
	}
	var applied applyResult
	if err = client.switchSubscription(ctx, second.ID, &applied); err != nil {
		t.Fatal(err)
	}
	var entries []subscriptionInfo
	if err = client.request(ctx, "GET", "/api/subscriptions", nil, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Applied || !entries[1].Applied {
		t.Fatalf("wrong current selection: %+v", entries)
	}
	disk, err := os.ReadFile(p.path)
	if err != nil || !strings.Contains(string(disk), "17996") || !strings.Contains(string(disk), "订阅乙") {
		t.Fatalf("switch did not preserve settings: %s %v", disk, err)
	}
	// Persisted current state survives a new panelServer and disappears if file changes.
	reopened := &panelServer{path: p.path}
	if reopened.activeSubscriptionID() != second.ID {
		t.Fatal("current subscription not persisted")
	}
	if _, err = client.applySubscription(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if p.activeSubscriptionID() != first.ID {
		t.Fatal("switch to first subscription not recorded")
	}
	reloadOK = false
	if result, err := client.applySubscription(ctx, first.ID); err == nil || result.Applied {
		t.Fatal("failed same-revision reload reported success")
	}
	if p.activeSubscriptionID() != "" {
		t.Fatal("same-revision reload failure retained current label")
	}
	if result, err := client.applySubscription(ctx, second.ID); err == nil || !result.Saved || result.Applied {
		t.Fatalf("failed reload reported success: %+v %v", result, err)
	}
	if p.activeSubscriptionID() != "" {
		t.Fatal("failed reload retained stale current marker")
	}
	before, _ := os.ReadFile(p.path)
	if err = client.request(ctx, "DELETE", "/api/subscriptions/"+entries[1].ID, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err = client.request(ctx, "GET", "/api/subscriptions", nil, &entries); err != nil || len(entries) != 1 {
		t.Fatalf("delete failed: %+v %v", entries, err)
	}
	after, _ := os.ReadFile(p.path)
	if string(after) != string(before) {
		t.Fatal("deleting subscription changed running config")
	}
}

func TestTerminalSubscriptionCurrentLabelInvalidatedByManualEdit(t *testing.T) {
	p := testCorePanel(t)
	content := "rules: [\"MATCH,DIRECT\"]\n"
	os.WriteFile(p.path, []byte(content), 0600)
	data, _ := json.Marshal(activeSubscription{ID: "current", Revision: revision([]byte(content))})
	os.WriteFile(p.path+".active-subscription.json", data, 0600)
	if p.activeSubscriptionID() != "current" {
		t.Fatal("current marker unreadable")
	}
	os.WriteFile(p.path, []byte(content+"mode: global\n"), 0600)
	if p.activeSubscriptionID() != "" {
		t.Fatal("manual edit still labeled as active subscription")
	}
}
