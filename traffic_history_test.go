package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func trafficCounters(at int64, download, upload uint64) *overviewCounters {
	return &overviewCounters{SampledAt: at, Downloaded: download, Uploaded: upload}
}

func TestTrafficHistoryRatesAndGaps(t *testing.T) {
	var history trafficHistory
	const start = int64(1_000_000)
	history.observe(trafficCounters(start, 100, 50), start)
	history.observe(trafficCounters(start+3000, 3100, 1550), start+3000)
	history.observe(trafficCounters(start+6000, 6100, 3050), start+6000)
	snapshot := history.snapshot(start + 6000)
	if len(snapshot.Samples) != 2 || !snapshot.Samples[0].StartsSegment || snapshot.Samples[1].StartsSegment ||
		snapshot.Rate == nil || snapshot.Rate.Download != 1000 || snapshot.Rate.Upload != 500 {
		t.Fatalf("unexpected continuous traffic: %+v", snapshot)
	}
	snapshot.Samples[0].Download = 999
	if history.snapshot(start + 6000).Samples[0].Download != 1000 {
		t.Fatal("API callers can mutate the stored history")
	}
	history.observe(nil, start+9000)
	if history.snapshot(start+9000).Rate != nil {
		t.Fatal("an outage must not keep reporting a live rate")
	}
	history.observe(trafficCounters(start+12000, 9000, 4500), start+12000)
	history.observe(trafficCounters(start+15000, 12000, 6000), start+15000)
	snapshot = history.snapshot(start + 15000)
	if len(snapshot.Samples) != 3 || !snapshot.Samples[2].StartsSegment {
		t.Fatalf("an outage must create a real gap, not an interpolated rate: %+v", snapshot)
	}
	// A core restart resets the cumulative counters and must start a new segment.
	history.observe(trafficCounters(start+18000, 10, 5), start+18000)
	if history.snapshot(start+18000).Rate != nil {
		t.Fatal("counter resets must invalidate the current rate")
	}
	history.observe(trafficCounters(start+21000, 3010, 1505), start+21000)
	snapshot = history.snapshot(start + 21000)
	if len(snapshot.Samples) != 4 || !snapshot.Samples[3].StartsSegment || snapshot.Rate.Download != 1000 {
		t.Fatalf("counter reset recovery: %+v", snapshot)
	}
	if history.snapshot(start+28000).Rate != nil {
		t.Fatal("stale samples must not be presented as the current rate")
	}
	// A long gap between successful reads is also an outage, even without a nil read.
	history.observe(trafficCounters(start+30000, 6010, 3005), start+30000)
	if len(history.snapshot(start+30000).Samples) != 4 {
		t.Fatal("long sampling gaps must not be averaged into a traffic point")
	}
}

func TestTrafficHistoryWindowIsBounded(t *testing.T) {
	var history trafficHistory
	var now int64
	for i := range 500 {
		now = 1_000_000 + int64(i)*connectionProbePeriod.Milliseconds()
		history.observe(trafficCounters(now, uint64(i)*3000, uint64(i)*1500), now)
	}
	snapshot := history.snapshot(now)
	if len(snapshot.Samples) != 43 || snapshot.Samples[0].Time < now-(trafficHistoryWindow+2*connectionProbePeriod).Milliseconds() {
		t.Fatalf("history window was not bounded: %+v", snapshot)
	}
	if expired := history.snapshot(now + (trafficHistoryWindow + time.Minute).Milliseconds()); len(expired.Samples) != 0 || expired.Rate != nil {
		t.Fatalf("expired points were retained: %+v", expired)
	}
}

func TestTrafficSampledWithoutOpenOverview(t *testing.T) {
	var reads atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			_, _ = w.Write([]byte(`{"version":"test"}`))
		case "/connections":
			n := reads.Add(1)
			_, _ = fmt.Fprintf(w, `{"downloadTotal":%d,"uploadTotal":%d,"connections":[]}`, n*3000, n*1500)
		case "/memory":
			_, _ = w.Write([]byte(`{"inuse":1000}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	panel := &panelServer{control: upstream.URL, client: upstream.Client(),
		connectionHistory: openConnectionHistory(filepath.Join(t.TempDir(), "config.yaml"), upstream.URL)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); panel.monitorConnection(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.NewTimer(9 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for len(panel.trafficHistory.snapshot(time.Now().UnixMilli()).Samples) < 2 {
		select {
		case <-deadline.C:
			t.Fatal("traffic did not accumulate without an open overview page")
		case <-tick.C:
		}
	}
	// Opening and reopening the overview reads the same process-owned history.
	for range 2 {
		result := httptest.NewRecorder()
		panel.runtimeOverview(result, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
		var overview runtimeOverview
		if err := json.Unmarshal(result.Body.Bytes(), &overview); err != nil || len(overview.Traffic.Samples) < 2 {
			t.Fatalf("overview did not return the accumulated traffic: %s (%v)", result.Body, err)
		}
	}
}
