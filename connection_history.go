package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"
)

const (
	maxConnectionEvents   = 1000
	maxConnectionHistory  = 256 << 10
	connectionProbePeriod = 3 * time.Second
)

type connectionEvent struct {
	ID        uint64 `json:"id"`
	Connected bool   `json:"connected"`
	Time      int64  `json:"time"`
}

type connectionHistoryFile struct {
	Connected *bool             `json:"connected"`
	Events    []connectionEvent `json:"events"` // Oldest first.
}

type connectionHistorySnapshot struct {
	Revision       string            `json:"revision"`
	Events         []connectionEvent `json:"events"`
	StorageWarning string            `json:"storageWarning,omitempty"`
}

type connectionHistory struct {
	mu         sync.Mutex
	path       string
	data       connectionHistoryFile
	revision   int64
	readFailed bool
	needsSave  bool
	warning    string
}

func openConnectionHistory(configPath, controller string) *connectionHistory {
	// Keep each controller's history when the panel changes controller addresses.
	addressHash := sha256.Sum256([]byte(controller))
	path := fmt.Sprintf("%s.connection-history-%x.json", configPath, addressHash[:8])
	history := &connectionHistory{path: path, revision: time.Now().UnixNano()}
	data, err := readConnectionHistory(path)
	switch {
	case err == nil:
		history.data = data
	case !errors.Is(err, os.ErrNotExist):
		history.readFailed = true
		history.warning = "无法读取历史文件，本次记录仅在面板运行期间可用。"
		log.Printf("Read connection history: %v", err)
	}
	return history
}

func readConnectionHistory(path string) (connectionHistoryFile, error) {
	var data connectionHistoryFile
	info, err := os.Lstat(path)
	if err != nil {
		return data, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxConnectionHistory {
		return data, errors.New("expected a regular connection history file up to 256 KiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return data, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxConnectionHistory+1))
	if err != nil {
		return data, err
	}
	if len(content) > maxConnectionHistory {
		return data, errors.New("connection history exceeds 256 KiB")
	}
	if err := json.Unmarshal(content, &data); err != nil {
		return data, err
	}
	if data.Connected == nil {
		return data, errors.New("connection history is missing its connection state")
	}
	var lastID uint64
	for _, event := range data.Events {
		if event.ID <= lastID || event.Time <= 0 {
			return data, errors.New("connection history contains an invalid event")
		}
		lastID = event.ID
	}
	if len(data.Events) > maxConnectionEvents {
		data.Events = slices.Clone(data.Events[len(data.Events)-maxConnectionEvents:])
	}
	return data, nil
}

func (h *connectionHistory) observe(connected bool, time int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	before := h.data.Connected
	if before == nil || *before != connected {
		if before != nil {
			id := uint64(1)
			if last := len(h.data.Events) - 1; last >= 0 {
				id = h.data.Events[last].ID + 1
			}
			if len(h.data.Events) == maxConnectionEvents {
				copy(h.data.Events, h.data.Events[1:])
				h.data.Events = h.data.Events[:maxConnectionEvents-1]
			}
			h.data.Events = append(h.data.Events, connectionEvent{ID: id, Connected: connected, Time: time})
			h.revision++
		}
		h.data.Connected = &connected
		h.needsSave = true
	}
	if h.readFailed || !h.needsSave {
		return
	}
	warning := ""
	if err := h.save(); err != nil {
		warning = "暂时无法保存记录，正在重试。"
		if h.warning == "" {
			log.Print(err)
		}
	} else {
		h.needsSave = false
	}
	if warning != h.warning {
		h.warning = warning
		h.revision++
	}
}

func (h *connectionHistory) save() error {
	content, err := json.Marshal(h.data)
	if err != nil {
		return fmt.Errorf("encode connection history: %w", err)
	}
	if err := atomicWrite(h.path, content, 0600); err != nil {
		return fmt.Errorf("save connection history: %w", err)
	}
	return nil
}

func (h *connectionHistory) snapshot(knownRevision string) *connectionHistorySnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	revision := strconv.FormatInt(h.revision, 10)
	if revision == knownRevision {
		return nil
	}
	events := make([]connectionEvent, len(h.data.Events))
	copy(events, h.data.Events)
	slices.Reverse(events)
	return &connectionHistorySnapshot{Revision: revision, Events: events, StorageWarning: h.warning}
}

func (p *panelServer) monitorConnection(ctx context.Context) {
	ticker := time.NewTicker(connectionProbePeriod)
	defer ticker.Stop()
	for {
		probe, cancel := context.WithTimeout(ctx, connectionProbePeriod)
		connection := p.connection()
		probe = context.WithValue(probe, coreConnectionContextKey{}, connection)
		connected := p.controllerVersion(probe) != ""
		var counters *overviewCounters
		if connected {
			counters = p.overviewCounters(probe)
		}
		cancel()
		if ctx.Err() != nil {
			return
		}
		if connection.History != nil {
			connection.History.observe(connected, time.Now().UnixMilli())
		}
		connection.Traffic.observe(counters, time.Now().UnixMilli())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
