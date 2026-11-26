package server

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestCoreDownloadReportsByteProgress(t *testing.T) {
	p := testCorePanel(t)
	r := &coreDownloadReader{source: strings.NewReader("downloaded core"), manager: p.core, total: 15, label: "下载内核"}
	if _, err := io.Copy(io.Discard, r); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p.coreState(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	json.Unmarshal(data, &state)
	if state["downloaded"] != float64(15) || state["total"] != float64(15) {
		t.Fatalf("download progress missing: %s", data)
	}
	if speed, ok := state["speed"].(float64); !ok || speed <= 0 {
		t.Fatalf("download speed missing: %s", data)
	}
}
