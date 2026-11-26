package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const maxSubscriptions = 12

type usage struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
	Total    int64 `json:"total"`
	Expire   int64 `json:"expire"`
}

type subscriptionInfo struct {
	Applied bool      `json:"applied,omitempty"`
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Kind    string    `json:"kind"`
	Updated time.Time `json:"updated"`
	Usage   *usage    `json:"usage,omitempty"`
}

type subscription struct {
	subscriptionInfo
	URL     string `json:"url,omitempty"`
	Content string `json:"content"`
}

func (p *panelServer) readSubscriptions() ([]subscription, error) {
	file, err := os.Open(p.path + ".subscriptions.json")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var entries []subscription
	err = json.NewDecoder(file).Decode(&entries)
	return entries, err
}

func (p *panelServer) writeSubscriptions(entries []subscription) error {
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	return atomicWrite(p.path+".subscriptions.json", data, 0600)
}

func (p *panelServer) listSubscriptions(w http.ResponseWriter, _ *http.Request) {
	entries, err := p.readSubscriptions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取订阅失败。", err)
		return
	}
	result := make([]subscriptionInfo, 0, len(entries))
	activeID := p.activeSubscriptionID()
	for _, entry := range entries {
		entry.Applied = entry.ID == activeID
		result = append(result, entry.subscriptionInfo)
	}
	writeJSON(w, http.StatusOK, result)
}

func (p *panelServer) getSubscription(w http.ResponseWriter, r *http.Request) {
	entries, err := p.readSubscriptions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取订阅失败。", err)
		return
	}
	for _, entry := range entries {
		if entry.ID == r.PathValue("id") {
			writeJSON(w, http.StatusOK, map[string]string{"content": entry.Content})
			return
		}
	}
	writeError(w, http.StatusNotFound, "订阅已不存在。", nil)
}

func (p *panelServer) addSubscription(w http.ResponseWriter, r *http.Request) {
	var input struct{ Name, URL, Content string }
	if !readJSON(w, r, &input, maxConfig*6+8192) {
		return
	}
	name := strings.TrimSpace(input.Name)
	rawURL := strings.TrimSpace(input.URL)
	if name == "" || utf8.RuneCountInString(name) > 100 || strings.ContainsAny(name, "\r\n") ||
		(rawURL == "") == (input.Content == "") {
		writeError(w, http.StatusBadRequest, "请填写订阅名称和链接，或选择 YAML 文件。", nil)
		return
	}
	entry := subscription{subscriptionInfo: subscriptionInfo{Name: name, Kind: "file"}, URL: rawURL, Content: input.Content}
	if rawURL != "" {
		entry.Kind = "url"
		var err error
		entry.Content, entry.Usage, err = p.downloadSubscription(r.Context(), rawURL)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "下载订阅失败。", err)
			return
		}
	}
	if err := checkSubscription(entry.Content); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "无法导入订阅。", err)
		return
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		writeError(w, http.StatusInternalServerError, "无法创建订阅。", err)
		return
	}
	entry.ID = hex.EncodeToString(id[:])
	entry.Updated = time.Now()

	p.library.Lock()
	defer p.library.Unlock()
	entries, err := p.readSubscriptions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取订阅失败。", err)
		return
	}
	for _, existing := range entries {
		if rawURL != "" && existing.URL == rawURL {
			writeError(w, http.StatusConflict, "这个链接已导入，可以更新已有订阅。", nil)
			return
		}
	}
	if len(entries) >= maxSubscriptions {
		writeError(w, http.StatusConflict, "最多可保存 12 份订阅。", nil)
		return
	}
	if err := p.writeSubscriptions(append(entries, entry)); err != nil {
		writeError(w, http.StatusInternalServerError, "保存订阅失败。", err)
		return
	}
	writeJSON(w, http.StatusCreated, entry.subscriptionInfo)
}

func (p *panelServer) updateSubscription(w http.ResponseWriter, r *http.Request) {
	if !readJSON(w, r, &struct{}{}, 1024) {
		return
	}
	entries, err := p.readSubscriptions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取订阅失败。", err)
		return
	}
	for _, entry := range entries {
		if entry.ID != r.PathValue("id") {
			continue
		}
		if entry.URL == "" {
			writeError(w, http.StatusBadRequest, "文件订阅可通过重新导入更新。", nil)
			return
		}
		content, info, err := p.downloadSubscription(r.Context(), entry.URL)
		if err == nil {
			err = checkSubscription(content)
		}
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "更新失败，已保留上次下载。", err)
			return
		}
		p.changeSubscription(w, entry.ID, func(entries []subscription, index int) ([]subscription, any) {
			if !entries[index].Updated.Equal(entry.Updated) {
				return entries, nil
			}
			entry.Content, entry.Usage, entry.Updated = content, info, time.Now()
			entries[index] = entry
			return entries, entry.subscriptionInfo
		})
		return
	}
	writeError(w, http.StatusNotFound, "订阅已不存在。", nil)
}

func (p *panelServer) deleteSubscription(w http.ResponseWriter, r *http.Request) {
	p.changeSubscription(w, r.PathValue("id"), func(entries []subscription, index int) ([]subscription, any) {
		return append(entries[:index], entries[index+1:]...), map[string]bool{"deleted": true}
	})
}

func (p *panelServer) changeSubscription(w http.ResponseWriter, id string, change func([]subscription, int) ([]subscription, any)) {
	p.library.Lock()
	defer p.library.Unlock()
	entries, err := p.readSubscriptions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取订阅失败。", err)
		return
	}
	for index, entry := range entries {
		if entry.ID != id {
			continue
		}
		next, result := change(entries, index)
		if result == nil {
			writeError(w, http.StatusConflict, "订阅已更新，请刷新列表。", nil)
			return
		}
		if err := p.writeSubscriptions(next); err != nil {
			writeError(w, http.StatusInternalServerError, "保存订阅失败。", err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	writeError(w, http.StatusNotFound, "订阅已不存在。", nil)
}

func checkSubscription(content string) error {
	if len(content) == 0 || len(content) > maxConfig || !utf8.ValidString(content) {
		return errors.New("订阅须为 2 MiB 以内的 UTF-8 YAML。")
	}
	decoder := yaml.NewDecoder(strings.NewReader(content))
	var doc, extra yaml.Node
	if decoder.Decode(&doc) != nil || decoder.Decode(&extra) != io.EOF ||
		len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return errors.New("订阅须包含一份 Mihomo YAML 配置。")
	}
	fields := doc.Content[0].Content
	seen := make(map[string]bool)
	var hasProxies bool
	for index := 0; index < len(fields); index += 2 {
		key, value := fields[index], fields[index+1]
		if key.Kind != yaml.ScalarNode || seen[key.Value] {
			return errors.New("订阅的顶层字段必须是唯一的名称。")
		}
		seen[key.Value] = true
		if value.Kind == yaml.AliasNode && value.Alias != nil {
			value = value.Alias
		}
		switch key.Value {
		case "proxies":
			hasProxies = hasProxies || value.Kind == yaml.SequenceNode && len(value.Content) > 0
		case "proxy-providers":
			hasProxies = hasProxies || value.Kind == yaml.MappingNode && len(value.Content) > 0
		}
	}
	if !hasProxies {
		return errors.New("未找到代理节点或代理集合，请使用机场提供的 Clash / Mihomo YAML。")
	}
	return nil
}

func (p *panelServer) downloadSubscription(ctx context.Context, rawURL string) (string, *usage, error) {
	data, headers, err := p.downloadPublicFile(ctx, rawURL)
	if err != nil {
		return "", nil, err
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	return string(data), parseUsage(headers.Get("Subscription-Userinfo")), nil
}

func (p *panelServer) downloadPublicFile(ctx context.Context, rawURL string) ([]byte, http.Header, error) {
	if !validSubscriptionURL(rawURL) {
		return nil, nil, errors.New("请输入有效的公网 HTTP(S) 订阅链接。")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, errors.New("订阅链接格式无效。")
	}
	// Advertise Clash.Meta so providers return Mihomo YAML with modern protocols.
	request.Header.Set("User-Agent", "Clash.Meta (MiProxy Panel)")
	response, err := p.subscriptions.Do(request)
	if err != nil {
		return nil, nil, errors.New("无法连接订阅地址，请确认链接可从面板所在设备访问。")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("订阅服务器返回 HTTP %d。", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxConfig+1))
	if err != nil || len(data) > maxConfig {
		return nil, nil, errors.New("订阅读取失败或超过 2 MiB。")
	}
	return data, response.Header, nil
}

func parseUsage(header string) *usage {
	values := map[string]int64{}
	for _, item := range strings.Split(header, ";") {
		name, raw, ok := strings.Cut(item, "=")
		if n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); ok && err == nil && n >= 0 {
			values[strings.ToLower(strings.TrimSpace(name))] = n
		}
	}
	if values["total"] == 0 && values["expire"] == 0 {
		return nil
	}
	return &usage{values["upload"], values["download"], values["total"], values["expire"]}
}

func validSubscriptionURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && len(raw) <= 4096 && u.Hostname() != "" && u.User == nil &&
		u.Fragment == "" && u.Opaque == "" && (u.Scheme == "http" || u.Scheme == "https")
}

var nonPublicRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}
var publicIPv6 = netip.MustParsePrefix("2000::/3")

func publicAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || (address.Is6() && !publicIPv6.Contains(address)) {
		return false
	}
	for _, blocked := range nonPublicRanges {
		if blocked.Contains(address) {
			return false
		}
	}
	return true
}

func subscriptionClient() *http.Client {
	dialer := &net.Dialer{Timeout: 6 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(target)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			// Dial the checked address directly so a second DNS lookup cannot change the target.
			for _, address := range addresses {
				if publicAddress(address) {
					conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
					if err == nil {
						return conn, nil
					}
				}
			}
			return nil, errors.New("no reachable public address")
		},
		TLSHandshakeTimeout:    6 * time.Second,
		ResponseHeaderTimeout:  12 * time.Second,
		IdleConnTimeout:        20 * time.Second,
		MaxIdleConns:           8,
		MaxResponseHeaderBytes: 64 << 10,
	}
	return &http.Client{
		Transport: transport, Timeout: 22 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 || !validSubscriptionURL(request.URL.String()) ||
				(via[len(via)-1].URL.Scheme == "https" && request.URL.Scheme == "http") {
				return errors.New("invalid subscription redirect")
			}
			// The previous URL may contain the subscription token.
			request.Header.Del("Referer")
			return nil
		},
	}
}
