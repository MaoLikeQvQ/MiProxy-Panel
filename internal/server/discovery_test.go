package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiscoveryCoreArgumentsPreservePathsAndSecretOverrides(t *testing.T) {
	home := filepath.Join(t.TempDir(), "Application Support", "Sparkle")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, "runtime config.yaml")
	if err := os.WriteFile(config, []byte("external-controller: 0.0.0.0:9090\nsecret: config-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, collapsed := range []bool{false, true} {
		t.Run(fmt.Sprint(collapsed), func(t *testing.T) {
			args := []string{"-d", home, "--config=" + config, "-ext-ctl", "127.0.0.1:9091", "--secret=command secret"}
			if collapsed {
				args = splitCoreCommand("-d " + home + " --config=" + config + " -ext-ctl 127.0.0.1:9091 --secret='command secret'")
			}
			found := processCoreCandidates(discoveredCoreProcess{Binary: "/Applications/Sparkle.app/Contents/MacOS/mihomo", Args: args, CollapsedArgs: collapsed, Cwd: "/"})
			if len(found) != 1 {
				t.Fatalf("found %d candidates", len(found))
			}
			c := found[0]
			if c.Source != "Sparkle" {
				t.Fatalf("source=%q, want Sparkle", c.Source)
			}
			if c.Home != home || c.Config != config || c.Controller != "127.0.0.1:9091" || c.Secret != "command secret" || !c.Running {
				t.Fatalf("failed path or override parsing (home=%q, config=%q, controller=%q)", c.Home, c.Config, c.Controller)
			}
		})
	}
	inherited := processCoreCandidates(discoveredCoreProcess{Args: []string{"-f", config, "-ext-ctl", "127.0.0.1:9092"}, Cwd: "/"})
	if inherited[0].Secret != "config-secret" {
		t.Fatal("controller override discarded config authentication")
	}
}

func TestDiscoveryNormalizesOnlyLocalEndpoints(t *testing.T) {
	cases := map[string]string{
		"0.0.0.0:9090":                 "http://127.0.0.1:9090",
		":9090":                        "http://127.0.0.1:9090",
		"http://localhost:9090/":       "http://127.0.0.1:9090",
		"[::]:9090":                    "http://[::1]:9090",
		"unix:/tmp/space dir/api.sock": "unix:/tmp/space dir/api.sock",
	}
	for input, want := range cases {
		got, err := normalizeDiscoveredController(input)
		if err != nil || got != want {
			t.Fatalf("normalize %q=%q,%v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"http://192.168.1.1:9090", "http://example.com:9090", "http://127.0.0.1:9090/path", "http://token@127.0.0.1:9090", "unix:relative.sock", "http://127.0.0.1:9090?token=private"} {
		if _, err := normalizeDiscoveredController(input); err == nil {
			t.Fatalf("unsafe endpoint accepted: %s", input)
		}
	}
}

func TestDiscoveryDeduplicatesAPIAndTriesMatchingCredentials(t *testing.T) {
	var accepted atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			t.Error("unexpected discovery route")
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer correct-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		accepted.Add(1)
		fmt.Fprint(w, `{"version":"v-test"}`)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"version":"v-other"}`) }))
	defer second.Close()
	candidates := []coreCandidate{
		{Source: "first", Binary: "/mihomo", Controller: first.URL, Secret: "wrong-secret"},
		{Source: "duplicate", Binary: "/mihomo", Controller: first.URL, Secret: "correct-secret", Running: true, PID: 42},
		{Source: "other", Binary: "/other/mihomo", Controller: second.URL},
	}
	found := collectCoreCandidates(context.Background(), nil, candidates)
	if len(found) != 2 {
		t.Fatalf("duplicate API produced %d candidates", len(found))
	}
	var connected *coreCandidate
	for i := range found {
		if found[i].Controller == first.URL {
			connected = &found[i]
		}
	}
	if connected == nil || !connected.Connected || !connected.Running || connected.Secret != "correct-secret" || connected.PID != 42 {
		t.Fatal("matching authentication or running state was lost")
	}
	encoded, err := json.Marshal(found)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"wrong-secret", "correct-secret", `"home"`, `"pid"`, `"secrets"`} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("private field leaked: %s", private)
		}
	}
	if accepted.Load() != 1 {
		t.Fatalf("same API successfully probed %d times", accepted.Load())
	}
	again := collectCoreCandidates(context.Background(), nil, candidates)
	if found[0].ID != again[0].ID || found[1].ID != again[1].ID {
		t.Fatal("candidate IDs changed between identical scans")
	}
}

func TestDiscoveryUnauthorizedCoreRemainsRunning(t *testing.T) {
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer controller.Close()
	found := collectCoreCandidates(context.Background(), nil, []coreCandidate{{Binary: "/mihomo", Controller: controller.URL, Secret: "private-secret"}})
	if len(found) != 1 || found[0].Connected || !found[0].Running || !strings.Contains(found[0].Reason, "认证失败") {
		t.Fatal("unreachable authenticated core was treated as a stopped install")
	}
}

func TestDiscoveryUnixSocketController(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "miproxy-discovery-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "control api.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer socket-secret" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, `{"version":"socket-core"}`)
	})}
	go server.Serve(listener)
	defer server.Close()
	config := filepath.Join(dir, "config.yaml")
	data := "external-controller-unix: '" + socket + "'\nsecret: socket-secret\n"
	if err := os.WriteFile(config, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	found := collectCoreCandidates(context.Background(), nil, []coreCandidate{{Binary: "/mihomo", Config: config, Home: dir}})
	if len(found) != 1 || !found[0].Connected || found[0].Version != "socket-core" || found[0].Controller != "unix:"+socket {
		t.Fatal("unix control socket was not discovered")
	}
	encoded, _ := json.Marshal(found)
	if strings.Contains(string(encoded), "socket-secret") {
		t.Fatal("socket secret leaked")
	}
}

func TestDiscoveryLinuxProcFixture(t *testing.T) {
	root := t.TempDir()
	process := filepath.Join(root, "1234")
	if err := os.Mkdir(process, 0700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home space")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(home, "mihomo")
	for path, value := range map[string]string{filepath.Join(process, "comm"): "mihomo\n", filepath.Join(process, "cmdline"): strings.Join([]string{binary, "-d", home, "-ext-ctl-unix", "/tmp/fixture api.sock", "-secret", "private"}, "\x00") + "\x00"} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(binary, filepath.Join(process, "exe")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(home, filepath.Join(process, "cwd")); err != nil {
		t.Fatal(err)
	}
	found, warnings := scanProcCores(context.Background(), root)
	if len(found) != 1 || len(warnings) != 0 || found[0].PID != 1234 || found[0].Binary != binary || found[0].Cwd != home {
		t.Fatal("proc fixture was not scanned correctly")
	}
	candidate := processCoreCandidates(found[0])[0]
	if candidate.Home != home || candidate.Controller != "unix:/tmp/fixture api.sock" || candidate.Secret != "private" {
		t.Fatal("proc argv boundaries were lost")
	}
}

func TestDiscoveryPSAndLsofFixtures(t *testing.T) {
	pid, path, ok := parseCorePSLine("  123 /Applications/Some App.app/Contents/MacOS/mihomo")
	if !ok || pid != 123 || path != "/Applications/Some App.app/Contents/MacOS/mihomo" {
		t.Fatal("ps executable path lost spaces")
	}
	found := parseDiscoveredEndpoints("p123\nn*:9090\nn[::1]:9091\nn192.168.1.2:9092\n", "p123\nn/tmp/private api.sock\n")
	want := []string{"unix:/tmp/private api.sock", "http://127.0.0.1:9090", "http://[::1]:9091"}
	if !reflect.DeepEqual(found, want) {
		t.Fatalf("lsof endpoints=%v, want %v", found, want)
	}
}

func TestDiscoveryProbeHonorsDeadlineAndRejectsRedirect(t *testing.T) {
	var destination atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destination.Add(1)
		fmt.Fprint(w, `{"version":"unexpected"}`)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	found := collectCoreCandidates(context.Background(), nil, []coreCandidate{{Binary: "/mihomo", Controller: redirect.URL, Secret: "private"}})
	if len(found) != 1 || found[0].Connected || destination.Load() != 0 {
		t.Fatal("discovery followed a controller redirect")
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	found = collectCoreCandidates(ctx, nil, []coreCandidate{{Binary: "/mihomo", Controller: slow.URL, Running: true}})
	if time.Since(start) > time.Second || len(found) != 1 || found[0].Connected || !found[0].Running {
		t.Fatal("probe deadline or running state was not preserved")
	}
}

func TestDiscoveryInfersManagingApplicationFromProcessPaths(t *testing.T) {
	cases := []struct {
		name, binary, config, home, want string
	}{
		{"Sparkle binary", "/Applications/Sparkle.app/Contents/MacOS/mihomo", "", "", "Sparkle"},
		{"Sparkle home", "/usr/local/bin/mihomo", "", "/Users/test/Library/Application Support/Sparkle", "Sparkle"},
		{"ShellCrash config", "/usr/bin/mihomo", "/etc/ShellCrash/yamls/config.yaml", "", "ShellCrash"},
		{"OpenClash binary", "/etc/openclash/core/clash_meta", "", "", "OpenClash"},
		{"Clash Verge binary", "/Applications/Clash Verge.app/Contents/MacOS/verge-mihomo", "", "", "Clash Verge"},
		{"Clash Verge home", "/usr/bin/mihomo", "", "/home/test/.config/io.github.clash-verge-rev.clash-verge-rev", "Clash Verge"},
		{"generic binary", "/usr/bin/mihomo", "/etc/mihomo/config.yaml", "/etc/mihomo", "运行中的 Mihomo"},
		{"unrelated username", "/Users/sparkle/bin/mihomo", "", "", "运行中的 Mihomo"},
	}
	for _, fixture := range cases {
		t.Run(fixture.name, func(t *testing.T) {
			if got := discoveredCoreSource(fixture.binary, fixture.config, fixture.home); got != fixture.want {
				t.Fatalf("source=%q, want %q", got, fixture.want)
			}
		})
	}
}
