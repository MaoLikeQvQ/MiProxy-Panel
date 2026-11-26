package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"gopkg.in/yaml.v3"
)

func (p *panelServer) getOverride(w http.ResponseWriter, _ *http.Request) {
	snapshot, err := readConfigFile(p.path + ".override.yaml")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取覆写失败。", err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (p *panelServer) saveOverride(w http.ResponseWriter, r *http.Request) {
	var input configInput
	if !readJSON(w, r, &input, maxConfig*6+1024) {
		return
	}
	if len(input.Content) > maxConfig {
		writeError(w, http.StatusBadRequest, "覆写文件超过 2 MiB。", nil)
		return
	}
	if strings.TrimSpace(input.Content) != "" {
		decoder := yaml.NewDecoder(strings.NewReader(input.Content))
		var doc, extra yaml.Node
		if decoder.Decode(&doc) != nil || decoder.Decode(&extra) != io.EOF ||
			len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
			writeError(w, http.StatusUnprocessableEntity, "覆写须为单份 YAML 映射。", nil)
			return
		}
	}

	p.library.Lock()
	defer p.library.Unlock()
	path := p.path + ".override.yaml"
	current, err := readConfigFile(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取覆写失败。", err)
		return
	}
	if input.Revision != current.Revision {
		writeError(w, http.StatusConflict, "覆写已有更新，请重新读取后再保存。", nil)
		return
	}
	if err := atomicWrite(path, []byte(input.Content), 0600); err != nil {
		writeError(w, http.StatusInternalServerError, "保存覆写失败。", err)
		return
	}
	writeJSON(w, http.StatusOK, configSnapshot{
		Content: input.Content, Revision: revision([]byte(input.Content)), Exists: true,
	})
}

type runtimeGroup struct {
	Name string   `json:"name"`
	Type string   `json:"type"`
	Now  string   `json:"now"`
	All  []string `json:"all"`
}

type proxySelection struct {
	Group string `json:"group"`
	Name  string `json:"name"`
}

func (p *panelServer) runtimeGroups(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	response, err := p.request(ctx, http.MethodGet, "/proxies", nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "无法连接 Mihomo 控制接口。", nil)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, "Mihomo 无法返回代理组，请检查控制接口设置。", nil)
		return
	}

	var data struct {
		Proxies map[string]struct {
			runtimeGroup
			Hidden bool `json:"hidden"`
		}
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&data); err != nil || data.Proxies == nil {
		writeError(w, http.StatusBadGateway, "Mihomo 返回了无效的代理组数据。", nil)
		return
	}
	groups := make([]runtimeGroup, 0)
	for _, proxy := range data.Proxies {
		if !proxy.Hidden && proxy.All != nil {
			groups = append(groups, proxy.runtimeGroup)
		}
	}
	writeJSON(w, http.StatusOK, groups)
}

func (p *panelServer) selectProxy(w http.ResponseWriter, r *http.Request) {
	var input proxySelection
	if !readJSON(w, r, &input, 8192) {
		return
	}
	if input.Group == "" || input.Group == "." || input.Group == ".." || input.Name == "" {
		writeError(w, http.StatusBadRequest, "请选择代理组和节点。", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	payload, _ := json.Marshal(map[string]string{"name": input.Name})
	response, err := p.request(ctx, http.MethodPut, "/proxies/"+url.PathEscape(input.Group), bytes.NewReader(payload))
	if err != nil {
		writeError(w, http.StatusBadGateway, "无法连接 Mihomo 控制接口。", nil)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		writeError(w, http.StatusBadGateway, "Mihomo 未能切换节点，请刷新代理组后重试。", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"selected": true})
}

func (p *panelServer) measureNodeDelay(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &input, 8192) {
		return
	}
	if strings.TrimSpace(input.Name) == "" || input.Name == "." || input.Name == ".." || strings.ContainsAny(input.Name, "\r\n\x00") {
		writeError(w, http.StatusBadRequest, "请选择要测速的节点。", nil)
		return
	}
	query := url.Values{
		"url":      {"https://www.gstatic.com/generate_204"},
		"timeout":  {"4000"},
		"expected": {"204"},
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	response, err := p.request(ctx, http.MethodGet, "/proxies/"+url.PathEscape(input.Name)+"/delay?"+query.Encode(), nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "无法连接 Mihomo 测速接口。", nil)
		return
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusGatewayTimeout:
		writeJSON(w, http.StatusOK, map[string]string{"status": "timeout"})
	case http.StatusServiceUnavailable:
		writeJSON(w, http.StatusOK, map[string]string{"status": "unreachable"})
	case http.StatusNotFound:
		writeError(w, http.StatusNotFound, "节点已变更，请刷新代理组。", nil)
	case http.StatusOK:
		var result struct {
			Delay uint16 `json:"delay"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result) != nil || result.Delay == 0 {
			writeError(w, http.StatusBadGateway, "Mihomo 返回了无效的延迟。", nil)
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusBadGateway, "Mihomo 测速请求失败。", nil)
	}
}

func (p *panelServer) runtimeRuleProviders(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	response, err := p.request(ctx, http.MethodGet, "/providers/rules", nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "无法连接 Mihomo 控制接口。", nil)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, "无法读取 Mihomo 的规则集状态。", nil)
		return
	}
	var data struct {
		Providers map[string]struct {
			UpdatedAt time.Time `json:"updatedAt"`
		} `json:"providers"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&data); err != nil || data.Providers == nil {
		writeError(w, http.StatusBadGateway, "Mihomo 返回了无效的规则集状态。", nil)
		return
	}
	writeJSON(w, http.StatusOK, data.Providers)
}

func (p *panelServer) updateRuleProvider(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &input, 8192) {
		return
	}
	if strings.TrimSpace(input.Name) == "" || input.Name == "." || input.Name == ".." || strings.ContainsAny(input.Name, "\r\n\x00") {
		writeError(w, http.StatusBadRequest, "请选择要更新的规则集。", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	response, err := p.request(ctx, http.MethodPut, "/providers/rules/"+url.PathEscape(input.Name), nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "规则集更新请求失败，请检查控制接口或稍后重试。", nil)
		return
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusNoContent:
		writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
	case http.StatusNotFound:
		writeError(w, http.StatusNotFound, "规则集尚未在 Mihomo 中生效，请先保存并应用。", nil)
	default:
		writeError(w, http.StatusBadGateway, "Mihomo 更新规则集失败，请检查规则集地址和日志。", nil)
	}
}

func (p *panelServer) streamLogs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	connection := p.connection()
	header := make(http.Header)
	if connection.Secret != "" {
		header.Set("Authorization", "Bearer "+connection.Secret)
	}
	conn, _, err := websocket.Dial(ctx, connection.Control+"/logs?level=debug", &websocket.DialOptions{
		HTTPClient: connection.Client,
		HTTPHeader: header,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "无法连接 Mihomo 日志，请检查控制接口设置。", nil)
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(256 << 10)

	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	if controller.Flush() != nil {
		return
	}
	_ = controller.SetWriteDeadline(time.Time{})
	for {
		if p.connection().ID != connection.ID {
			return
		}
		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		data = bytes.TrimSpace(data)
		if !json.Valid(data) {
			continue
		}
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return
		}
		if _, err := w.Write(append(data, '\n')); err != nil || controller.Flush() != nil {
			return
		}
		_ = controller.SetWriteDeadline(time.Time{})
	}
}
