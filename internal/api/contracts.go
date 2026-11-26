// Package api defines the JSON contracts shared by the server and terminal.
package api

import "time"

type CoreStatus struct {
	Connected      bool    `json:"connected"`
	Version        string  `json:"version,omitempty"`
	Installed      bool    `json:"installed"`
	Binary         string  `json:"binary,omitempty"`
	Platform       string  `json:"platform"`
	Managed        bool    `json:"managed"`
	AutoStart      bool    `json:"autoStart"`
	Busy           bool    `json:"busy"`
	Phase          string  `json:"phase"`
	Detail         string  `json:"detail,omitempty"`
	DisabledReason string  `json:"disabledReason,omitempty"`
	Downloaded     int64   `json:"downloaded"`
	Total          int64   `json:"total"`
	Speed          float64 `json:"speed"`
	SelectedID     string  `json:"selectedId"`
	Source         string  `json:"source"`
	External       bool    `json:"external"`
}

type Counters struct {
	Active     int    `json:"active"`
	Downloaded uint64 `json:"downloaded"`
	Uploaded   uint64 `json:"uploaded"`
	SampledAt  int64  `json:"sampledAt"`
}

type Overview struct {
	Controller string             `json:"controller"`
	Version    string             `json:"version"`
	Connected  bool               `json:"connected"`
	Counters   *Counters          `json:"counters,omitempty"`
	Memory     *uint64            `json:"memory,omitempty"`
	History    *ConnectionHistory `json:"history,omitempty"`
	Traffic    Traffic            `json:"traffic"`
}

type Usage struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
	Total    int64 `json:"total"`
	Expire   int64 `json:"expire"`
}

type Subscription struct {
	Applied bool      `json:"applied,omitempty"`
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Kind    string    `json:"kind"`
	Updated time.Time `json:"updated"`
	Usage   *Usage    `json:"Usage,omitempty"`
}

type Group struct {
	Name string   `json:"name"`
	Type string   `json:"type"`
	Now  string   `json:"now"`
	All  []string `json:"all"`
}

type ProxySelection struct {
	Group string `json:"group"`
	Name  string `json:"name"`
}

type ApplyResult struct {
	Saved    bool   `json:"saved"`
	Applied  bool   `json:"applied"`
	Backup   bool   `json:"backup"`
	Revision string `json:"revision"`
	Detail   string `json:"detail,omitempty"`
}

type ConnectionEvent struct {
	ID        uint64 `json:"id"`
	Connected bool   `json:"connected"`
	Time      int64  `json:"time"`
}

type ConnectionHistory struct {
	Revision       string            `json:"revision"`
	Events         []ConnectionEvent `json:"events"`
	StorageWarning string            `json:"storageWarning,omitempty"`
}

type TrafficSample struct {
	Time          int64   `json:"time"`
	Download      float64 `json:"download"`
	Upload        float64 `json:"upload"`
	StartsSegment bool    `json:"startsSegment"`
}

type Traffic struct {
	Samples []TrafficSample `json:"samples"`
	Rate    *TrafficSample  `json:"rate,omitempty"`
}

type CoreCandidate struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	Binary     string `json:"binary,omitempty"`
	Config     string `json:"config,omitempty"`
	Controller string `json:"controller,omitempty"`
	Running    bool   `json:"running"`
	Connected  bool   `json:"connected"`
	Version    string `json:"version,omitempty"`
	Reason     string `json:"reason,omitempty"`
}
