package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

type runtimeRule struct {
	Index   int        `json:"index"`
	Type    string     `json:"type"`
	Payload string     `json:"payload"`
	Proxy   string     `json:"proxy"`
	Extra   *ruleExtra `json:"extra,omitempty"`
}

type ruleExtra struct {
	Disabled bool   `json:"disabled"`
	HitCount uint64 `json:"hitCount"`
}

func (p *panelServer) readRuntimeRules(ctx context.Context) ([]runtimeRule, error) {
	response, err := p.request(ctx, http.MethodGet, "/rules", nil)
	if err != nil {
		return nil, errors.New("无法连接 Mihomo 控制接口。")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("无法读取 Mihomo 当前生效的规则。")
	}
	var data struct {
		Rules []runtimeRule `json:"rules"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&data); err != nil || data.Rules == nil {
		return nil, errors.New("Mihomo 返回了无效的规则列表。")
	}
	// Older cores can omit index. The response order is the rule matching order.
	for index := range data.Rules {
		data.Rules[index].Index = index
	}
	return data.Rules, nil
}

func (p *panelServer) runtimeRules(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rules, err := p.readRuntimeRules(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

func (p *panelServer) setRuntimeRuleState(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Index    *int   `json:"index"`
		Type     string `json:"type"`
		Payload  string `json:"payload"`
		Proxy    string `json:"proxy"`
		Disabled *bool  `json:"disabled"`
	}
	if !readJSON(w, r, &input, maxConfig) {
		return
	}
	if input.Index == nil || *input.Index < 0 || input.Type == "" || input.Proxy == "" || input.Disabled == nil {
		writeError(w, http.StatusBadRequest, "请选择有效的规则及其运行状态。", nil)
		return
	}
	if !p.save.TryLock() {
		writeError(w, http.StatusConflict, "正在应用配置，请稍后刷新运行规则。", nil)
		return
	}
	defer p.save.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	rules, err := p.readRuntimeRules(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), nil)
		return
	}
	index := *input.Index
	if index >= len(rules) || rules[index].Type != input.Type ||
		rules[index].Payload != input.Payload || rules[index].Proxy != input.Proxy {
		writeError(w, http.StatusConflict, "Mihomo 规则已变更，请刷新后重试。", nil)
		return
	}
	if rules[index].Extra == nil {
		writeError(w, http.StatusNotImplemented, "当前 Mihomo 不支持临时切换规则状态。", nil)
		return
	}
	body, _ := json.Marshal(map[int]bool{index: *input.Disabled})
	response, err := p.request(ctx, http.MethodPatch, "/rules/disable", bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusBadGateway, "无法连接 Mihomo 控制接口。", nil)
		return
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusNoContent:
		writeJSON(w, http.StatusOK, map[string]bool{"disabled": *input.Disabled})
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		writeError(w, http.StatusNotImplemented, "当前 Mihomo 不支持临时切换规则状态。", nil)
	default:
		writeError(w, http.StatusBadGateway, "Mihomo 切换规则状态失败。", nil)
	}
}
