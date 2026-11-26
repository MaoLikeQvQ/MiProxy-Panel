package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestInspectRuleProvider(t *testing.T) {
	for _, test := range []struct {
		name, content, format, behavior string
	}{
		{"classical text with policy", "\ufeff# Home DNS\r\nDOMAIN-SUFFIX,example.com,REJECT\r\n", "text", "classical"},
		{"classical YAML", "payload:\n  - IP-CIDR,192.0.2.0/24,no-resolve\n  - DOMAIN,example.com\n", "yaml", "classical"},
		{"domain text", "// example list\n+.example.com\nsub.example.org\n", "text", "domain"},
		{"domain YAML", "payload: ['+.example.com', '*.example.org']", "yaml", "domain"},
		{"IPv4 and IPv6", "192.0.2.0/24\n2001:db8::/32", "text", "ipcidr"},
		{"mixed payload", "payload: ['+.example.com', '192.0.2.0/24']", "yaml", ""},
		{"HTML", "<html>not a rule list</html>", "", ""},
		{"proxy subscription", "proxies: [{name: Test, type: direct}]", "", ""},
		{"AdGuard syntax", "||example.com^\n", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			want := ruleProviderDetection{Format: test.format, Behavior: test.behavior}
			if got := inspectRuleProvider([]byte(test.content)); got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestInspectMRS(t *testing.T) {
	writer, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for code, want := range map[byte]string{0: "domain", 1: "ipcidr", 2: ""} {
		header := append([]byte("MRS\x01"), code)
		if got := inspectRuleProvider(writer.EncodeAll(header, nil)); got.Format != "mrs" || got.Behavior != want {
			t.Errorf("behavior byte %d: got %+v, want mrs/%s", code, got, want)
		}
	}
	if got := inspectRuleProvider(writer.EncodeAll([]byte("example.com"), nil)); got.Format != "" {
		t.Errorf("ordinary zstd file identified as %s", got.Format)
	}
}

func TestDetectRuleProviderDownloadsOnce(t *testing.T) {
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Method != http.MethodGet {
			t.Error("only a GET should be sent to the upstream")
		}
		_, _ = w.Write([]byte("DOMAIN-SUFFIX,example.com,REJECT"))
	}))
	defer upstream.Close()
	panel := &panelServer{subscriptions: upstream.Client()}
	body, err := json.Marshal(map[string]string{"url": upstream.URL + "/misleading.yaml?token=private"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/rule-providers/detect", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	panel.detectRuleProvider(res, req)

	var result ruleProviderDetection
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil || res.Code != http.StatusOK ||
		result.Format != "text" || result.Behavior != "classical" || hits != 1 || strings.Contains(res.Body.String(), "private") {
		t.Fatalf("requests %d, response %d %s", hits, res.Code, res.Body)
	}
}
