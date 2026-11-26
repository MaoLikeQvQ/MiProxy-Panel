package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type coreTransport func(*http.Request) (*http.Response, error)

func (f coreTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testCorePanel(t *testing.T) *panelServer {
	t.Helper()
	dir := t.TempDir()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := "http://" + probe.Addr().String()
	probe.Close()
	p := &panelServer{
		path: filepath.Join(dir, "config.yaml"), runtimePath: filepath.Join(dir, "config.yaml"), home: dir,
		mihomo: filepath.Join(dir, "missing-mihomo"), controlAddr: address, control: address,
		client: &http.Client{}, core: newCoreManager(context.Background()), secret: "test-core-secret",
	}
	t.Cleanup(p.closeCore)
	return p
}

func compressedCore(t *testing.T, content []byte) ([]byte, []byte) {
	t.Helper()
	var buffer bytes.Buffer
	w := gzip.NewWriter(&buffer)
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(buffer.Bytes())
	return buffer.Bytes(), digest[:]
}

func TestCoreInstallVerifiesReleaseAndKeepsPreviousBinary(t *testing.T) {
	p := testCorePanel(t)
	if err := os.MkdirAll(p.coreDirectory(), 0700); err != nil {
		t.Fatal(err)
	}
	archive, digest := compressedCore(t, []byte("verified binary"))
	platform, err := coreAssetPlatform()
	if err != nil {
		t.Skip(err)
	}
	assetURL := "https://github.com/MetaCubeX/mihomo/releases/download/v-test/mihomo.gz"
	corrupt := false
	p.core.download.Transport = coreTransport(func(r *http.Request) (*http.Response, error) {
		var body []byte
		switch r.URL.String() {
		case coreReleaseURL:
			body, _ = json.Marshal(map[string]any{
				"tag_name": "v-test", "assets": []map[string]string{{
					"name": "mihomo-" + platform + "-v-test.gz", "browser_download_url": assetURL,
					"digest": "sha256:" + hex.EncodeToString(digest),
				}},
			})
		case assetURL:
			body = append([]byte{}, archive...)
			if corrupt {
				body[len(body)-1] ^= 0xff
			}
		default:
			t.Fatalf("unexpected download: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	if err := p.installCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.coreDirectory(), "mihomo")
	if data, err := os.ReadFile(path); err != nil || string(data) != "verified binary" {
		t.Fatalf("installed = %q, %v", data, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0700 {
		t.Fatalf("unsafe binary mode: %v", info.Mode())
	}
	corrupt = true
	if err := p.installCore(context.Background()); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("corrupt download accepted: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "verified binary" {
		t.Fatal("failed install replaced existing binary")
	}
	files, _ := os.ReadDir(p.coreDirectory())
	if len(files) != 1 {
		t.Fatalf("temporary downloads leaked: %v", files)
	}
}

func TestCoreConnectDoesNotLaunchOverExistingController(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			p := testCorePanel(t)
			controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				if status == 200 {
					io.WriteString(w, `{"version":"existing"}`)
				}
			}))
			defer controller.Close()
			p.control, p.controlAddr = controller.URL, controller.URL
			p.core.download.Transport = coreTransport(func(r *http.Request) (*http.Response, error) {
				t.Error("attempted download despite existing process")
				return nil, fmt.Errorf("unexpected download")
			})
			err := p.setupCore(context.Background())
			if status == 200 && err != nil {
				t.Fatal(err)
			}
			if status == 401 && (err == nil || !strings.Contains(err.Error(), "端口")) {
				t.Fatalf("occupied API port = %v", err)
			}
			if p.core.command != nil {
				t.Fatal("started a duplicate controller")
			}
		})
	}
}

func TestCoreRejectsExternalManagementAndCrossSiteRequests(t *testing.T) {
	for _, kind := range []string{"external-config", "remote", "socket", "https", "mount"} {
		t.Run(kind, func(t *testing.T) {
			p := testCorePanel(t)
			switch kind {
			case "external-config":
				p.externalConfig = true
			case "remote":
				p.controlAddr = "http://mihomo:9090"
			case "socket":
				p.controlAddr = "unix:/tmp/test.sock"
			case "https":
				p.controlAddr = "https://127.0.0.1:9090"
			case "mount":
				p.runtimePath = "/another-container/config.yaml"
			}
			if p.coreDisabledReason() == "" {
				t.Fatal("external management was allowed")
			}
		})
	}
	p := testCorePanel(t)
	r := httptest.NewRequest("POST", "/api/core/connect", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	p.connectCore(w, r)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("cross-site setup accepted: %d", w.Code)
	}
}

func TestCoreSetupSingleJobAndCancellation(t *testing.T) {
	p := testCorePanel(t)
	started := make(chan struct{})
	p.core.download.Transport = coreTransport(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	if !p.beginCoreSetup() {
		t.Fatal("first setup was rejected")
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not begin")
	}
	if p.beginCoreSetup() {
		t.Fatal("concurrent setup was accepted")
	}
	p.closeCore()
	p.core.mu.Lock()
	defer p.core.mu.Unlock()
	if p.core.busy || p.core.phase != "error" {
		t.Fatalf("cancelled job remained busy: %+v", p.core)
	}
}

// A subprocess exercises the real exec/start/readiness/shutdown path without downloading a binary.
func TestCoreHelperProcess(t *testing.T) {
	if os.Getenv("MIPROXY_TEST_CORE") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		os.Exit(2)
	}
	args = args[1:]
	if args[0] == "-v" {
		fmt.Println("Mihomo test-core")
		os.Exit(0)
	}
	if args[0] == "-t" {
		os.Exit(0)
	}
	address := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-ext-ctl" {
			address = args[i+1]
		}
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+os.Getenv("CLASH_OVERRIDE_SECRET") {
			w.WriteHeader(401)
			return
		}
		io.WriteString(w, `{"version":"test-core"}`)
	})
	if err := http.ListenAndServe(address, handler); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestCoreStartsConnectsAndRestoresManagedInstance(t *testing.T) {
	t.Setenv("MIPROXY_TEST_CORE", "1")
	p := testCorePanel(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p.mihomo = filepath.Join(p.home, "test-mihomo")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "' -test.run=^TestCoreHelperProcess$ -- \"$@\"\n"
	if err := os.WriteFile(p.mihomo, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for attempt := range 2 {
		if err := p.setupCore(context.Background()); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		state := p.coreState(context.Background())
		if !state.Connected || !state.Managed || !state.AutoStart || !state.Installed {
			t.Fatalf("bad state: %+v", state)
		}
		content, err := os.ReadFile(p.path)
		if err != nil || !strings.Contains(string(content), "MATCH,DIRECT") {
			t.Fatalf("initial config: %s, %v", content, err)
		}
		if err := p.validate(context.Background(), string(content)); err != nil {
			t.Fatal(err)
		}
		p.stopManagedCore()
		if p.coreState(context.Background()).Connected {
			t.Fatal("managed process survived stop")
		}
	}
}

func TestCoreFailedStartPreservesUserConfig(t *testing.T) {
	p := testCorePanel(t)
	const content = "mixed-port: 18888\nrules: [MATCH,DIRECT]\n"
	if err := os.WriteFile(p.path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	p.mihomo = filepath.Join(p.home, "test-mihomo")
	if err := os.WriteFile(p.mihomo, []byte("#!/bin/sh\ncase \"$1\" in -v) echo Mihomo;; -t) exit 0;; *) echo startup-failed; exit 1;; esac\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := p.setupCore(context.Background()); err == nil || !strings.Contains(err.Error(), "startup-failed") {
		t.Fatalf("failed start: %v", err)
	}
	if data, _ := os.ReadFile(p.path); string(data) != content {
		t.Fatal("user config was overwritten")
	}
	if _, err := os.Stat(filepath.Join(p.coreDirectory(), "enabled")); !os.IsNotExist(err) {
		t.Fatal("failed start enabled automatic startup")
	}
}

func TestCoreAssetPlatformSelectsConservativeArchitecture(t *testing.T) {
	for _, test := range []struct{ system, arch, arm, want string }{
		{"linux", "amd64", "", "linux-amd64-v1"}, {"darwin", "arm64", "", "darwin-arm64"},
		{"linux", "arm", "5", "linux-armv5"}, {"linux", "arm", "7", "linux-armv7"},
		{"linux", "mipsle", "", "linux-mipsle-softfloat"}, {"windows", "amd64", "", ""},
		{"linux", "loong64", "", ""},
	} {
		got, err := assetPlatform(test.system, test.arch, test.arm)
		if got != test.want || (err != nil) != (test.want == "") {
			t.Fatalf("%+v: %s, %v", test, got, err)
		}
	}
}
