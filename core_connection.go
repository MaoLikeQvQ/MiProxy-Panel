package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Base settings stay immutable. A selection swaps one complete connection so
// concurrent status, log and control requests never see half-updated credentials.
type coreConnection struct {
	ID, Source, Binary, Config, Home string
	Address, Control, Secret         string
	External                         bool
	Client                           *http.Client
	History                          *connectionHistory
	Traffic                          *trafficHistory
}

type savedCoreConnection struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	Binary     string `json:"binary,omitempty"`
	Config     string `json:"config,omitempty"`
	Home       string `json:"home,omitempty"`
	Controller string `json:"controller"`
	Secret     string `json:"secret,omitempty"`
	External   bool   `json:"external"`
}

type coreConnectionContextKey struct{}

func (p *panelServer) connection() *coreConnection {
	if selected := p.selection.Load(); selected != nil {
		return selected
	}
	return &coreConnection{
		ID: "panel", Source: "面板配置", Home: p.home, Config: p.path,
		Address: p.controlAddr, Control: p.control, Secret: p.secret, Client: p.client,
		External: p.externalConfig, History: p.connectionHistory, Traffic: &p.trafficHistory,
	}
}

func (p *panelServer) connectionFor(ctx context.Context) *coreConnection {
	if connection, ok := ctx.Value(coreConnectionContextKey{}).(*coreConnection); ok {
		return connection
	}
	return p.connection()
}

func (p *panelServer) connectionHome(ctx context.Context) string {
	if home := p.connectionFor(ctx).Home; home != "" {
		return home
	}
	return p.home
}

func (p *panelServer) connectionPath() string { return p.path + ".core-connection.json" }

func (p *panelServer) restoreCoreConnection() error {
	path := p.connectionPath()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return errors.New("内核连接记录须为 64 KiB 以内的普通文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	var saved savedCoreConnection
	if err := json.NewDecoder(io.LimitReader(file, (64<<10)+1)).Decode(&saved); err != nil {
		return errors.New("内核连接记录无效，请重新检测并选择内核")
	}
	if saved.ID == "" || saved.ID == "panel" || saved.Binary != "" && !filepath.IsAbs(saved.Binary) ||
		saved.Config != "" && !filepath.IsAbs(saved.Config) || saved.Home != "" && !filepath.IsAbs(saved.Home) {
		return errors.New("内核连接记录中的路径无效，请重新检测并选择内核")
	}
	if saved.External && !localCoreController(saved.Controller) {
		return errors.New("自动接入记录必须使用本机控制接口")
	}
	if saved.Config == p.path && saved.Controller == p.controlAddr {
		// Older versions could save the panel's own discovered process as external.
		return os.Remove(path)
	}
	connection, err := p.makeCoreConnection(saved)
	if err != nil {
		return errors.New("已保存的控制接口无效，请重新检测并选择内核")
	}
	p.selection.Store(connection)
	return nil
}

func localCoreController(address string) bool {
	if socket, ok := cutUnixController(address); ok {
		return filepath.IsAbs(socket)
	}
	parsed, err := url.Parse(address)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && isLoopback(parsed.Hostname())
}

func cutUnixController(address string) (string, bool) {
	const prefix = "unix:"
	if len(address) >= len(prefix) && address[:len(prefix)] == prefix {
		return address[len(prefix):], true
	}
	return "", false
}

func (p *panelServer) makeCoreConnection(saved savedCoreConnection) (*coreConnection, error) {
	if !saved.External {
		saved.Controller, saved.Secret, saved.Home, saved.Config = p.controlAddr, p.secret, p.home, p.path
	}
	control, client, err := controllerClient(saved.Controller)
	if err != nil {
		return nil, err
	}
	return &coreConnection{
		ID: saved.ID, Source: saved.Source, Binary: saved.Binary, Config: saved.Config, Home: saved.Home,
		Address: saved.Controller, Control: control, Client: client, Secret: saved.Secret, External: saved.External,
		History: openConnectionHistory(p.path, saved.Controller), Traffic: &trafficHistory{},
	}, nil
}

func (p *panelServer) listCoreDiscovery(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	candidates, warnings := p.discoverCores(ctx)
	if candidates == nil {
		candidates = []coreCandidate{}
	}
	if warnings == nil {
		warnings = []string{}
	}
	connection := p.connection()
	type visibleCandidate struct {
		coreCandidate
		Selected bool `json:"selected"`
	}
	visible := make([]visibleCandidate, 0, len(candidates))
	cache := make(map[string]coreCandidate, len(candidates))
	for _, candidate := range candidates {
		// A panel-owned configuration is an alternative deployment, not another
		// external app to connect to after selecting an existing core.
		if connection.External && candidate.Config == p.path && candidate.Controller == p.controlAddr {
			continue
		}
		if candidate.Source == "当前面板" && candidate.Binary == "" && !candidate.Running && !candidate.Connected {
			continue
		}
		cache[candidate.ID] = candidate
		selected := candidate.ID == connection.ID || candidate.Controller != "" && candidate.Controller == connection.Address
		if !connection.External && connection.Binary != "" && candidate.Binary == connection.Binary {
			selected = true
		}
		visible = append(visible, visibleCandidate{candidate, selected})
	}
	p.discoveryMu.Lock()
	p.discoveryCandidates = cache
	if p.discoveryWarning != "" {
		warnings = append(warnings, p.discoveryWarning)
	}
	p.discoveryMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"candidates": visible, "warnings": warnings, "selectedId": connection.ID})
}

func (p *panelServer) selectCoreConnection(w http.ResponseWriter, r *http.Request) {
	var input struct{ ID string }
	if !readJSON(w, r, &input, 1024) {
		return
	}
	if !p.save.TryLock() {
		writeError(w, http.StatusConflict, "正在应用配置，请稍后切换内核。", nil)
		return
	}
	defer p.save.Unlock()
	if p.core != nil {
		p.core.mu.Lock()
		busy := p.core.busy
		p.core.mu.Unlock()
		if busy {
			writeError(w, http.StatusConflict, "内核正在安装或启动，请稍后切换。", nil)
			return
		}
	}
	if input.ID == "panel" {
		if err := os.Remove(p.connectionPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusInternalServerError, "无法清除已保存的连接。", nil)
			return
		}
		p.selection.Store(nil)
		writeJSON(w, http.StatusOK, map[string]any{"selected": true, "selectedId": "panel"})
		return
	}
	p.discoveryMu.Lock()
	candidate, found := p.discoveryCandidates[input.ID]
	p.discoveryMu.Unlock()
	if !found {
		writeError(w, http.StatusConflict, "检测结果已失效，请重新检测后选择内核。", nil)
		return
	}
	if candidate.Running && !candidate.Connected {
		writeError(w, http.StatusConflict, "该内核正在运行但控制接口不可用，请按检测提示处理。", nil)
		return
	}
	if candidate.Config == p.path && candidate.Controller == p.controlAddr {
		// Discovery also sees our own child process. Never turn it into an
		// external connection, which would lose the ownership distinction.
		if err := os.Remove(p.connectionPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusInternalServerError, "无法清除已保存的连接。", nil)
			return
		}
		p.selection.Store(nil)
		writeJSON(w, http.StatusOK, map[string]any{"selected": true, "selectedId": "panel"})
		return
	}
	if !candidate.Connected && candidate.Binary == "" {
		writeError(w, http.StatusConflict, "该候选没有可用的控制接口或本机内核。", nil)
		return
	}
	saved := savedCoreConnection{
		ID: candidate.ID, Source: candidate.Source, Binary: candidate.Binary, Config: candidate.Config, Home: candidate.Home,
		Controller: candidate.Controller, Secret: candidate.Secret, External: candidate.Connected,
	}
	if !saved.External {
		// Reusing a binary uses the panel's settings, not the other app's credentials.
		saved.Config, saved.Home, saved.Controller, saved.Secret = "", "", "", ""
	}
	if saved.External && !localCoreController(saved.Controller) {
		writeError(w, http.StatusConflict, "自动接入仅支持本机控制接口。", nil)
		return
	}
	connection, err := p.makeCoreConnection(saved)
	if err != nil {
		writeError(w, http.StatusConflict, "无法连接检测到的内核，请重新检测。", nil)
		return
	}
	if saved.External {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		ctx = context.WithValue(ctx, coreConnectionContextKey{}, connection)
		if p.controllerVersion(ctx) == "" {
			writeError(w, http.StatusConflict, "该内核已无法连接，请重新检测后重试。", nil)
			return
		}
	} else if p.core != nil {
		p.core.mu.Lock()
		running := p.core.command != nil
		p.core.mu.Unlock()
		if running {
			writeError(w, http.StatusConflict, "面板已有运行中的内核，请使用当前内核或连接已有实例。", nil)
			return
		}
	}
	data, err := json.Marshal(saved)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(p.path), 0700)
	}
	if err == nil {
		err = atomicWrite(p.connectionPath(), data, 0600)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法保存内核连接设置。", nil)
		return
	}
	p.selection.Store(connection)
	if connection.External && p.core != nil {
		// Only the process started by this panel is stopped. The selected
		// external core stays owned by its original application.
		p.stopManagedCore()
	}
	writeJSON(w, http.StatusOK, map[string]any{"selected": true, "selectedId": connection.ID})
}
