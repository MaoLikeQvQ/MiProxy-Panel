package main

import (
	"slices"
	"sync"
	"time"
)

const trafficHistoryWindow = 2 * time.Minute

type trafficSample struct {
	Time          int64   `json:"time"`
	Download      float64 `json:"download"`
	Upload        float64 `json:"upload"`
	StartsSegment bool    `json:"startsSegment"`
}

type trafficSnapshot struct {
	Samples []trafficSample `json:"samples"`
	Rate    *trafficSample  `json:"rate,omitempty"`
}

// Sampling belongs to the panel process so hidden tabs and page reloads do not
// create gaps. Only a short in-memory window is needed for the live chart.
type trafficHistory struct {
	mu         sync.Mutex
	previous   *overviewCounters
	samples    []trafficSample
	continuous bool
}

func (h *trafficHistory) prune(now int64) {
	cutoff := now - (trafficHistoryWindow + 2*connectionProbePeriod).Milliseconds()
	first := 0
	for first < len(h.samples) && h.samples[first].Time < cutoff {
		first++
	}
	h.samples = slices.Delete(h.samples, 0, first)
}

func (h *trafficHistory) observe(counters *overviewCounters, now int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prune(now)
	before := h.previous
	h.previous = counters
	if before == nil || counters == nil {
		h.continuous = false
		return
	}
	elapsed := counters.SampledAt - before.SampledAt
	if elapsed <= 0 || elapsed > (2*connectionProbePeriod).Milliseconds() ||
		counters.Downloaded < before.Downloaded || counters.Uploaded < before.Uploaded {
		h.continuous = false
		return
	}
	seconds := float64(elapsed) / 1000
	h.samples = append(h.samples, trafficSample{
		Time: counters.SampledAt, Download: float64(counters.Downloaded-before.Downloaded) / seconds,
		Upload: float64(counters.Uploaded-before.Uploaded) / seconds, StartsSegment: !h.continuous,
	})
	h.continuous = true
}

func (h *trafficHistory) snapshot(now int64) trafficSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prune(now)
	result := trafficSnapshot{Samples: append([]trafficSample{}, h.samples...)}
	if last := len(result.Samples) - 1; last >= 0 && h.continuous &&
		now-result.Samples[last].Time <= (2*connectionProbePeriod).Milliseconds() {
		latest := result.Samples[last]
		result.Rate = &latest
	}
	return result
}
