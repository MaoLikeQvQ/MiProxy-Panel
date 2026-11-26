package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func selectCandidateForTest(p *panelServer, id string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"id": id})
	r := httptest.NewRequest(http.MethodPost, "/api/core/select", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	p.selectCoreConnection(w, r)
	return w
}

func TestExternalSelectionStopsOnlyPanelOwnedProcess(t *testing.T) {
	p := testCorePanel(t)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"sparkle-test"}`))
	}))
	defer controller.Close()
	command := exec.Command("sleep", "30")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	p.core.mu.Lock()
	p.core.command, p.core.done = command, done
	p.core.mu.Unlock()
	go func() {
		command.Wait()
		p.core.mu.Lock()
		p.core.command = nil
		p.core.mu.Unlock()
		close(done)
	}()
	p.discoveryCandidates = map[string]coreCandidate{"sparkle": {
		ID: "sparkle", Source: "Sparkle", Running: true, Connected: true, Controller: controller.URL,
	}}
	if result := selectCandidateForTest(p, "unknown"); result.Code != http.StatusConflict {
		t.Fatal("invalid selection accepted")
	}
	select {
	case <-done:
		t.Fatal("failed selection stopped current core")
	default:
	}
	if result := selectCandidateForTest(p, "sparkle"); result.Code != http.StatusOK {
		t.Fatalf("selection failed: %s", result.Body.String())
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("previous panel core still running")
	}
	if p.controllerVersion(context.Background()) != "sparkle-test" || p.connection().Source != "Sparkle" {
		t.Fatal("selected core was not Sparkle")
	}
}

func TestSelectingDiscoveredOwnCoreKeepsPanelOwnership(t *testing.T) {
	p := testCorePanel(t)
	p.discoveryCandidates = map[string]coreCandidate{"own-process": {
		ID: "own-process", Source: "运行中的 Mihomo", Config: p.path, Controller: p.controlAddr, Running: true, Connected: true,
	}}
	result := selectCandidateForTest(p, "own-process")
	if result.Code != http.StatusOK || p.connection().External || p.connection().ID != "panel" {
		t.Fatal("own process became external")
	}
	if !strings.Contains(result.Body.String(), `"selectedId":"panel"`) {
		t.Fatal("response omitted canonical panel selection")
	}
	old := savedCoreConnection{ID: "own-process", Config: p.path, Controller: p.controlAddr, External: true}
	data, _ := json.Marshal(old)
	if err := os.WriteFile(p.connectionPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.restoreCoreConnection(); err != nil {
		t.Fatal(err)
	}
	if p.connection().External {
		t.Fatal("old incorrect ownership record restored")
	}
	if _, err := os.Stat(p.connectionPath()); !os.IsNotExist(err) {
		t.Fatal("incorrect ownership record not removed")
	}
}

func TestSelectExistingCorePersistsWithoutTakingOverConfig(t *testing.T) {
	p := testCorePanel(t)
	const secret = "private-controller-secret"
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("selected controller did not receive its own credential")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/version" {
			t.Error("selection must not write or reload external runtime")
		}
		w.Write([]byte(`{"version":"external-test"}`))
	}))
	defer controller.Close()
	externalPath := filepath.Join(t.TempDir(), "external.yaml")
	const original = "rules: [MATCH,REJECT]\n"
	if err := os.WriteFile(externalPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	p.discoveryCandidates = map[string]coreCandidate{
		"external": {ID: "external", Source: "已有内核", Controller: controller.URL, Secret: secret,
			Config: externalPath, Home: filepath.Dir(externalPath), Connected: true, Running: true},
	}
	result := selectCandidateForTest(p, "external")
	if result.Code != http.StatusOK || strings.Contains(result.Body.String(), secret) {
		t.Fatalf("selection=%d %s", result.Code, result.Body.String())
	}
	if p.controllerVersion(context.Background()) != "external-test" {
		t.Fatal("selected controller not used")
	}
	snapshot := httptest.NewRecorder()
	p.getConfig(snapshot, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if !strings.Contains(snapshot.Body.String(), `"externalConfig":true`) || !strings.Contains(snapshot.Body.String(), "MATCH,REJECT") {
		t.Fatalf("external source=%s", snapshot.Body.String())
	}
	apply := httptest.NewRecorder()
	p.applyConfig(apply, httptest.NewRequest(http.MethodPost, "/api/apply", nil))
	if apply.Code != http.StatusForbidden {
		t.Fatal("external config must not be writable")
	}
	if data, _ := os.ReadFile(externalPath); string(data) != original {
		t.Fatal("external config changed")
	}
	info, err := os.Stat(p.connectionPath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("connection credentials must be stored privately")
	}
	state, _ := json.Marshal(p.coreState(context.Background()))
	if strings.Contains(string(state), secret) || !strings.Contains(string(state), `"external":true`) {
		t.Fatalf("unsafe state: %s", state)
	}
	restored := testCorePanel(t)
	restored.path = p.path
	if err := restored.restoreCoreConnection(); err != nil {
		t.Fatal(err)
	}
	if restored.connection().ID != "external" || restored.controllerVersion(context.Background()) != "external-test" {
		t.Fatal("saved selection did not survive restart")
	}
	if result := selectCandidateForTest(restored, "panel"); result.Code != http.StatusOK {
		t.Fatal("cannot restore panel core")
	}
	if restored.connection().ID != "panel" {
		t.Fatal("panel connection not restored")
	}
	if _, err := os.Stat(p.connectionPath()); !os.IsNotExist(err) {
		t.Fatal("saved external selection was not cleared")
	}
}

func TestCoreSelectionRejectsStaleUnavailableAndForeignCandidates(t *testing.T) {
	p := testCorePanel(t)
	p.discoveryCandidates = map[string]coreCandidate{
		"locked":  {ID: "locked", Running: true},
		"foreign": {ID: "foreign", Running: true, Connected: true, Controller: "http://192.168.1.1:9090"},
	}
	stopped := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	stopped.Close()
	p.discoveryCandidates["stopped"] = coreCandidate{ID: "stopped", Running: true, Connected: true, Controller: stopped.URL}
	for _, id := range []string{"unknown", "locked", "foreign", "stopped"} {
		if result := selectCandidateForTest(p, id); result.Code != http.StatusConflict {
			t.Fatalf("%s accepted: %d", id, result.Code)
		}
		if p.connection().ID != "panel" {
			t.Fatal("failed choice changed active connection")
		}
	}
}

func TestCoreSelectionKeepsRequestsAndHistoriesBoundToTheirController(t *testing.T) {
	p := testCorePanel(t)
	makeConnection := func(id string) *coreConnection {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+id {
				t.Error("credential crossed controller boundary")
			}
			w.Write([]byte(`{"version":"` + id + `"}`))
		}))
		t.Cleanup(server.Close)
		connection, err := p.makeCoreConnection(savedCoreConnection{ID: id, Source: id, Controller: server.URL, Secret: id, External: true})
		if err != nil {
			t.Fatal(err)
		}
		return connection
	}
	one, two := makeConnection("one"), makeConnection("two")
	if one.History.path == two.History.path || one.Traffic == two.Traffic {
		t.Fatal("controller histories were shared")
	}
	var readers sync.WaitGroup
	p.selection.Store(one)
	for range 4 {
		readers.Go(func() {
			for range 10 {
				connection := p.connection()
				ctx := context.WithValue(context.Background(), coreConnectionContextKey{}, connection)
				if version := p.controllerVersion(ctx); version != connection.ID {
					t.Errorf("pinned controller changed: %s", version)
				}
			}
		})
	}
	for range 100 {
		p.selection.Store(two)
		p.selection.Store(one)
	}
	readers.Wait()
}
