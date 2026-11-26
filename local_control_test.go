package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func localSocketTestPath(t *testing.T) string {
	t.Helper()
	// macOS has a 104-byte Unix socket path limit, shorter than some test dirs.
	dir, err := os.MkdirTemp("/tmp", "miproxy-local-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "panel.sock")
}

func TestLocalControlUsesProtectedSocketAndCloses(t *testing.T) {
	path := localSocketTestPath(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); ok {
			t.Error("local client should not need login credentials")
		}
		_, _ = io.WriteString(w, "local status")
	})
	closeLocal, err := startLocalControl(mux, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeLocal)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket permissions: info=%v err=%v", info, err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get("http://localhost/api/status")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "local status" {
		t.Fatalf("response %d %q %v", response.StatusCode, body, err)
	}
	closeLocal()
	closeLocal()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket remains after close: %v", err)
	}
}

func TestLocalControlDoesNotOverwriteFilesOrLiveSockets(t *testing.T) {
	for _, kind := range []string{"file", "symlink", "socket"} {
		t.Run(kind, func(t *testing.T) {
			path := localSocketTestPath(t)
			var listener *net.UnixListener
			var err error
			switch kind {
			case "file":
				err = os.WriteFile(path, []byte("keep me"), 0600)
			case "symlink":
				err = os.Symlink("missing-target", path)
			case "socket":
				listener, err = net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
				if listener != nil {
					defer listener.Close()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			closeLocal, err := startLocalControl(http.NewServeMux(), path)
			if err == nil {
				closeLocal()
				t.Fatal("expected existing path to be refused")
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("existing path was replaced: %v", err)
			}
		})
	}
}

func TestLocalControlRecoversStaleSocket(t *testing.T) {
	path := localSocketTestPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	closeLocal, err := startLocalControl(http.NewServeMux(), path)
	if err != nil {
		t.Fatal(err)
	}
	closeLocal()
}

func TestLocalControlRequiresPrivateDirectory(t *testing.T) {
	path := localSocketTestPath(t)
	if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	closeLocal, err := startLocalControl(http.NewServeMux(), path)
	if err == nil {
		closeLocal()
		t.Fatal("expected non-private directory to be rejected")
	}
}

func TestLocalControlCleanupPreservesReplacementFile(t *testing.T) {
	path := localSocketTestPath(t)
	closeLocal, err := startLocalControl(http.NewServeMux(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeLocal)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	closeLocal()
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "replacement" {
		t.Fatalf("cleanup modified replacement: %q %v", content, err)
	}
}

func TestLocalControlPath(t *testing.T) {
	t.Setenv("PANEL_SOCKET", "")
	if got := localControlPath("/etc/mihomo/config.yaml"); got != "/etc/mihomo/.miproxy/panel.sock" {
		t.Fatalf("default path: %s", got)
	}
	t.Setenv("PANEL_SOCKET", "/tmp/private-panel/control.sock")
	if got := localControlPath("/etc/mihomo/config.yaml"); got != "/tmp/private-panel/control.sock" {
		t.Fatalf("configured path: %s", got)
	}
}
