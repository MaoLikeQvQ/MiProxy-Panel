package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoreUpdateStagesAndPreservesOldBinaryOnFailure(t *testing.T) {
	for _, failure := range []string{"", "checksum", "validation", "not-executable"} {
		t.Run(failure, func(t *testing.T) {
			p := testCorePanel(t)
			if err := os.MkdirAll(p.coreDirectory(), 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(p.coreDirectory(), "mihomo")
			const old = "#!/bin/sh\necho 'mihomo old'\n"
			if err := os.WriteFile(target, []byte(old), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p.path, []byte("rules: [MATCH,DIRECT]\n"), 0600); err != nil {
				t.Fatal(err)
			}
			candidate := "#!/bin/sh\nif [ \"$1\" = '-v' ]; then echo 'mihomo new'; exit 0; fi\nexit 0\n"
			if failure == "validation" {
				candidate = strings.Replace(candidate, "\nexit 0\n", "\necho invalid; exit 1\n", 1)
			}
			if failure == "not-executable" {
				candidate = "invalid binary"
			}
			archive, digest := compressedCore(t, []byte(candidate))
			platform, err := coreAssetPlatform()
			if err != nil {
				t.Skip(err)
			}
			p.core.download.Transport = coreTransport(func(r *http.Request) (*http.Response, error) {
				var body []byte
				if r.URL.String() == coreReleaseURL {
					body, _ = json.Marshal(map[string]any{"tag_name": "v-test", "assets": []map[string]string{{"name": "mihomo-" + platform + "-v-test.gz", "browser_download_url": "https://github.com/MetaCubeX/mihomo/releases/download/v-test/core.gz", "digest": "sha256:" + hex.EncodeToString(digest)}}})
				} else {
					body = append([]byte{}, archive...)
					if failure == "checksum" {
						body[len(body)-1] ^= 0xff
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body))}, nil
			})
			err = p.performCoreUpdate(context.Background())
			got, _ := os.ReadFile(target)
			if failure == "" {
				if err != nil || string(got) != candidate {
					t.Fatalf("update = %v, binary=%q", err, got)
				}
			} else if err == nil || string(got) != old {
				t.Fatalf("failed update changed old core: %v, %q", err, got)
			}
			files, _ := os.ReadDir(p.coreDirectory())
			if len(files) != 1 {
				t.Fatalf("staging files leaked: %v", files)
			}
		})
	}
}
func TestCoreUpdateRejectsExternalAndConcurrentJobs(t *testing.T) {
	p := testCorePanel(t)
	if err := os.MkdirAll(p.coreDirectory(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.coreDirectory(), "mihomo"), []byte("binary"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"external", "busy"} {
		p.externalConfig = kind == "external"
		p.core.busy = kind == "busy"
		request := httptest.NewRequest("POST", "/api/core/update", strings.NewReader("{}"))
		request.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		p.updateCore(w, request)
		if w.Code != http.StatusConflict {
			t.Fatalf("%s accepted: %d", kind, w.Code)
		}
	}
	p.core.busy = false
}
