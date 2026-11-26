package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidatePreparesGeoDataBeforeCallingMihomo(t *testing.T) {
	p := testCorePanel(t)
	p.mihomo = filepath.Join(p.home, "test-mihomo")
	script := "#!/bin/sh\nif [ ! -s \"" + filepath.Join(p.home, "geoip.metadb") + "\" ]; then echo 'missing MMDB'; exit 1; fi\nexit 0\n"
	if err := os.WriteFile(p.mihomo, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	p.core.download.Transport = coreTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if !strings.HasSuffix(r.URL.Path, "/geoip.metadb") {
			t.Fatalf("unexpected dependency: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader([]byte{0, 1, 2, 3}))}, nil
	})
	content := "sub-rules:\n  imported:\n    - GEOIP,CN,DIRECT\nrules:\n  - SUB-RULE,(NETWORK,TCP),imported\n"
	if err := p.validate(context.Background(), content); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("downloads=%d, want 1", calls)
	}
	if err := p.validate(context.Background(), content); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("cached geodata downloaded twice")
	}
}

func TestGeoDependenciesRecognizesRoutingAndDNS(t *testing.T) {
	cases := []struct {
		name, content string
		files         string
	}{
		{"direct", "rules: [MATCH,DIRECT]", ""},
		{"lan", "rules: ['GEOIP,LAN,DIRECT']", ""},
		{"logic and dat mode", "geodata-mode: true\nrules: ['AND,((GEOIP,CN),(GEOSITE,cn)),DIRECT']", "GeoIP.dat,GeoSite.dat"},
		{"dns policy", "dns: {enable: true, nameserver-policy: {'geosite:cn': 1.1.1.1}}", "GeoSite.dat"},
		{"dns fallback default", "dns: {enable: true, fallback: [1.1.1.1]}", "geoip.metadb"},
		{"dns fallback disabled", "dns: {enable: true, fallback: [1.1.1.1], fallback-filter: {geoip: false}}", ""},
		{"source ASN", "rules: ['SRC-IP-ASN,12345,DIRECT']", "ASN.mmdb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			deps, err := geoDependencies(c.content)
			if err != nil {
				t.Fatal(err)
			}
			var files []string
			for _, dep := range deps {
				files = append(files, dep.name)
			}
			if strings.Join(files, ",") != c.files {
				t.Fatalf("got %v, want %s", files, c.files)
			}
		})
	}
}

func TestGeoPreparationUsesCustomPublicSourceAndPreservesFailedDownload(t *testing.T) {
	p := testCorePanel(t)
	customURL := "https://data.example.test/custom-metadb?token=private"
	p.subscriptions = &http.Client{Transport: coreTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != customURL {
			t.Fatal("custom geox-url was not used")
		}
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("unavailable")), Header: make(http.Header)}, nil
	})}
	original := "rules: [MATCH,DIRECT]\n"
	if err := os.WriteFile(p.path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	content := "geox-url: {mmdb: '" + customURL + "'}\nrules: ['GEOIP,CN,DIRECT']\n"
	err := p.validate(context.Background(), content)
	if err == nil || !strings.Contains(err.Error(), "geoip.metadb") || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe or unclear download failure: %v", err)
	}
	data, err := os.ReadFile(p.path)
	if err != nil || string(data) != original {
		t.Fatal("preparation changed current config")
	}
	files, _ := os.ReadDir(p.home)
	for _, file := range files {
		if file.Name() != "config.yaml" {
			t.Fatalf("failed download left file %s", file.Name())
		}
	}
}

func TestGeoPreparationKeepsExistingMMDBAlias(t *testing.T) {
	p := testCorePanel(t)
	if err := os.WriteFile(filepath.Join(p.home, "Country.mmdb"), []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	p.core.download.Transport = coreTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("existing Country.mmdb should avoid download")
		return nil, nil
	})
	if err := p.prepareGeoData(context.Background(), "rules: ['GEOIP,CN,DIRECT']"); err != nil {
		t.Fatal(err)
	}
}
