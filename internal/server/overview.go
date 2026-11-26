package server

import (
	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/api"

	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

type overviewCounters = api.Counters

type runtimeOverview = api.Overview

func (p *panelServer) runtimeOverview(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	connection := p.connection()
	ctx = context.WithValue(ctx, coreConnectionContextKey{}, connection)
	address := connection.Address
	if address == "" {
		address = connection.Control
	}
	overview := runtimeOverview{Controller: address, Version: p.controllerVersion(ctx)}
	overview.Connected = overview.Version != ""
	if overview.Connected {
		var reads sync.WaitGroup
		reads.Go(func() { overview.Counters = p.overviewCounters(ctx) })
		reads.Go(func() { overview.Memory = p.overviewMemory(ctx) })
		reads.Wait()
	}
	if connection.History != nil {
		overview.History = connection.History.snapshot(r.URL.Query().Get("historyRevision"))
	}
	overview.Traffic = connection.Traffic.snapshot(time.Now().UnixMilli())
	writeJSON(w, http.StatusOK, overview)
}

func (p *panelServer) overviewCounters(ctx context.Context) *overviewCounters {
	response, err := p.request(ctx, http.MethodGet, "/connections", nil)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil
	}
	var snapshot struct {
		Connections []struct{} `json:"connections"`
		Downloaded  *uint64    `json:"downloadTotal"`
		Uploaded    *uint64    `json:"uploadTotal"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&snapshot); err != nil ||
		snapshot.Downloaded == nil || snapshot.Uploaded == nil {
		return nil
	}
	return &overviewCounters{
		Active: len(snapshot.Connections), Downloaded: *snapshot.Downloaded, Uploaded: *snapshot.Uploaded,
		SampledAt: time.Now().UnixMilli(),
	}
}

func (p *panelServer) overviewMemory(ctx context.Context) *uint64 {
	response, err := p.request(ctx, http.MethodGet, "/memory", nil)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4096))
	// Mihomo sends a synthetic zero before its first real memory reading.
	for range 2 {
		var sample struct {
			InUse uint64 `json:"inuse"`
		}
		if decoder.Decode(&sample) != nil {
			return nil
		}
		if sample.InUse > 0 {
			return &sample.InUse
		}
	}
	return nil
}
