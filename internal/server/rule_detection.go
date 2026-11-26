package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
	"gopkg.in/yaml.v3"
)

type ruleProviderDetection struct {
	Format   string `json:"format"`
	Behavior string `json:"behavior,omitempty"`
}

func (p *panelServer) detectRuleProvider(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL string `json:"url"`
	}
	if !readJSON(w, r, &input, 32<<10) {
		return
	}
	address := strings.TrimSpace(input.URL)
	if !validSubscriptionURL(address) {
		writeError(w, http.StatusBadRequest, "请输入有效的公网 HTTP(S) 规则集地址。", nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	data, _, err := p.downloadPublicFile(ctx, address)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), nil)
		return
	}
	result := inspectRuleProvider(data)
	if result.Format == "" {
		writeError(w, http.StatusUnprocessableEntity, "无法从响应内容确定规则集格式。", nil)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func inspectRuleProvider(data []byte) ruleProviderDetection {
	if bytes.HasPrefix(data, []byte{0x28, 0xb5, 0x2f, 0xfd}) {
		return inspectMrsHeader(data)
	}
	if !utf8.Valid(data) {
		return ruleProviderDetection{}
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if behavior := inspectRuleEntries(strings.Split(string(data), "\n")); behavior != "" {
		return ruleProviderDetection{Format: "text", Behavior: behavior}
	}
	// Mihomo YAML providers put their rules in a top-level payload sequence.
	// Inspect YAML nodes directly so aliases in remote content stay unexpanded.
	var doc, extra yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(&doc) != nil || decoder.Decode(&extra) != io.EOF ||
		len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return ruleProviderDetection{}
	}
	var payload *yaml.Node
	fields := doc.Content[0].Content
	for index := 0; index < len(fields); index += 2 {
		if fields[index].Value != "payload" {
			continue
		}
		if payload != nil || fields[index+1].Kind != yaml.SequenceNode {
			return ruleProviderDetection{}
		}
		payload = fields[index+1]
	}
	if payload == nil {
		return ruleProviderDetection{}
	}
	entries := make([]string, 0, len(payload.Content))
	for _, entry := range payload.Content {
		if entry.Kind != yaml.ScalarNode || entry.Tag != "!!str" {
			return ruleProviderDetection{Format: "yaml"}
		}
		entries = append(entries, entry.Value)
	}
	return ruleProviderDetection{Format: "yaml", Behavior: inspectRuleEntries(entries)}
}

func inspectMrsHeader(data []byte) ruleProviderDetection {
	// Mihomo's zstd stream starts with MRS v1 magic, followed by the behavior byte.
	reader, err := zstd.NewReader(bytes.NewReader(data), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(8<<20))
	if err != nil {
		return ruleProviderDetection{}
	}
	defer reader.Close()
	var header [5]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil || string(header[:4]) != "MRS\x01" {
		return ruleProviderDetection{}
	}
	switch header[4] {
	case 0:
		return ruleProviderDetection{Format: "mrs", Behavior: "domain"}
	case 1:
		return ruleProviderDetection{Format: "mrs", Behavior: "ipcidr"}
	default:
		return ruleProviderDetection{Format: "mrs"}
	}
}

func inspectRuleEntries(entries []string) string {
	var behavior string
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" || strings.HasPrefix(entry, "#") || strings.HasPrefix(entry, "//") {
			continue
		}
		detected := ruleEntryBehavior(entry)
		if detected == "" || behavior != "" && behavior != detected {
			return ""
		}
		behavior = detected
	}
	return behavior
}

var domainRulePattern = regexp.MustCompile(`(?i)^(?:[+*]?\.)?(?:[a-z0-9_*\-]+\.)+[a-z0-9_*\-]+\.?$`)

func ruleEntryBehavior(entry string) string {
	if kind, value, ok := strings.Cut(entry, ","); ok {
		if strings.TrimSpace(value) == "" {
			return ""
		}
		switch strings.ToUpper(strings.TrimSpace(kind)) {
		case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "DOMAIN-REGEX", "DOMAIN-WILDCARD", "GEOSITE",
			"GEOIP", "SRC-GEOIP", "IP-ASN", "SRC-IP-ASN", "IP-CIDR", "IP-CIDR6", "SRC-IP-CIDR", "IP-SUFFIX", "SRC-IP-SUFFIX",
			"SRC-PORT", "DST-PORT", "IN-PORT", "DSCP", "PROCESS-NAME", "PROCESS-PATH", "PROCESS-NAME-REGEX",
			"PROCESS-PATH-REGEX", "PROCESS-NAME-WILDCARD", "PROCESS-PATH-WILDCARD", "NETWORK", "UID", "IN-TYPE", "IN-USER",
			"IN-NAME", "REMATCH-NAME", "SUB-RULE", "AND", "OR", "NOT", "RULE-SET", "MATCH":
			return "classical"
		}
		return ""
	}
	if _, err := netip.ParsePrefix(entry); err == nil {
		return "ipcidr"
	}
	if _, err := netip.ParseAddr(entry); err != nil && domainRulePattern.MatchString(entry) {
		return "domain"
	}
	return ""
}
