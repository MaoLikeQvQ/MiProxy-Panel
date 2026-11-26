package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConnectionHistoryRestoresTransitions(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.yaml")
	const controller = "http://127.0.0.1:9090"
	history := openConnectionHistory(config, controller)
	history.observe(true, 1000)
	initial := history.snapshot("")
	history.observe(true, 2000)
	if len(initial.Events) != 0 || history.snapshot(initial.Revision) != nil {
		t.Fatal("the first observation and repeated states must not create events")
	}

	history = openConnectionHistory(config, controller)
	history.observe(false, 3000)
	history.observe(false, 4000)
	history.observe(true, 5000)
	history = openConnectionHistory(config, controller)
	history.observe(true, 6000)
	if snapshot := history.snapshot(""); len(snapshot.Events) != 2 || snapshot.StorageWarning != "" ||
		snapshot.Events[0] != (connectionEvent{ID: 2, Connected: true, Time: 5000}) ||
		snapshot.Events[1] != (connectionEvent{ID: 1, Connected: false, Time: 3000}) {
		t.Fatalf("unexpected restored history: %+v", snapshot)
	}
	if snapshot := openConnectionHistory(config, "unix:/tmp/other.sock").snapshot(""); len(snapshot.Events) != 0 {
		t.Fatal("different controllers should have independent history")
	}
}

func TestConnectionHistoryKeepsLatest1000(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.yaml")
	const controller = "http://127.0.0.1:9090"
	history := openConnectionHistory(config, controller)
	connected := true
	entries := make([]connectionEvent, maxConnectionEvents+1)
	for i := range entries {
		entries[i] = connectionEvent{ID: uint64(i + 1), Connected: i%2 == 0, Time: int64(i+1) * 1000}
	}
	data, err := json.Marshal(connectionHistoryFile{Connected: &connected, Events: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(history.path, data, 0600); err != nil {
		t.Fatal(err)
	}

	history = openConnectionHistory(config, controller)
	if events := history.snapshot("").Events; len(events) != maxConnectionEvents || events[0].ID != 1001 || events[999].ID != 2 {
		t.Fatalf("expected IDs 1001 through 2, got %d events", len(events))
	}
	history.observe(false, 1002000)
	if events := openConnectionHistory(config, controller).snapshot("").Events; len(events) != maxConnectionEvents ||
		events[0].ID != 1002 || events[999].ID != 3 {
		t.Fatalf("expected IDs 1002 through 3, got %d events", len(events))
	}
}

func TestConnectionHistoryRetriesSave(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	config := filepath.Join(dir, "config.yaml")
	const controller = "http://127.0.0.1:9090"
	history := openConnectionHistory(config, controller)
	history.observe(true, 1000)
	history.observe(false, 2000)
	if snapshot := history.snapshot(""); len(snapshot.Events) != 1 || snapshot.StorageWarning == "" {
		t.Fatalf("expected an in-memory event and a storage warning, got %+v", snapshot)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	history.observe(false, 3000)
	if snapshot := history.snapshot(""); snapshot.StorageWarning != "" || len(snapshot.Events) != 1 {
		t.Fatalf("expected the warning to clear after retry, got %+v", snapshot)
	}
	if events := openConnectionHistory(config, controller).snapshot("").Events; len(events) != 1 || events[0].Time != 2000 {
		t.Fatal("the retry must persist the original event")
	}
}
