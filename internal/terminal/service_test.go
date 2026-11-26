package terminal

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func terminalServiceFixture(t *testing.T, manager string) terminalService {
	t.Helper()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("service"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	s := terminalService{
		manager: manager, euid: func() int { return 0 },
		stat: func(string) (os.FileInfo, error) { return info, nil },
		run:  func(context.Context, string, ...string) (string, error) { return "", nil },
	}
	if manager == "systemd" {
		s.binary, s.servicePath = "/usr/local/bin/miproxy-panel", "/etc/systemd/system/miproxy-panel.service"
	} else {
		s.binary, s.servicePath = "/usr/bin/miproxy-panel", "/etc/init.d/miproxy-panel"
	}
	return s
}

func TestTerminalServiceActionUsesFixedArguments(t *testing.T) {
	for _, manager := range []string{"systemd", "procd"} {
		t.Run(manager, func(t *testing.T) {
			s := terminalServiceFixture(t, manager)
			var got []string
			s.run = func(_ context.Context, name string, args ...string) (string, error) {
				got = append([]string{name}, args...)
				return "", nil
			}
			if _, err := s.Action(context.Background(), "restart"); err != nil {
				t.Fatal(err)
			}
			want := []string{"systemctl", "restart", "miproxy-panel.service"}
			if manager == "procd" {
				want = []string{"/etc/init.d/miproxy-panel", "restart"}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("command = %v, want %v", got, want)
			}
			got = nil
			if _, err := s.Action(context.Background(), "restart; rm -rf /"); err == nil || got != nil {
				t.Fatal("invalid action reached command runner")
			}
			s.euid = func() int { return 1000 }
			if _, err := s.Action(context.Background(), "stop"); err == nil || got != nil {
				t.Fatal("unprivileged action reached command runner")
			}
		})
	}
}

func TestTerminalServiceStatusAndLogs(t *testing.T) {
	s := terminalServiceFixture(t, "systemd")
	s.run = func(_ context.Context, name string, args ...string) (string, error) {
		switch args[0] {
		case "is-active":
			return "active\n", nil
		case "is-enabled":
			return "enabled\n", nil
		default:
			if name != "journalctl" || !reflect.DeepEqual(args, []string{"-u", "miproxy-panel.service", "--no-pager", "-n", "80", "-o", "short-iso"}) {
				t.Fatalf("unexpected log command: %s %v", name, args)
			}
			return "recent panel logs", nil
		}
	}
	status := s.Status(context.Background())
	if !status.Installed || !status.Running || !status.Enabled || status.Manager != "systemd" {
		t.Fatalf("status = %+v", status)
	}
	if logs, err := s.Logs(context.Background()); err != nil || logs != "recent panel logs" {
		t.Fatalf("logs = %q, %v", logs, err)
	}
	s.stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	if status = s.Status(context.Background()); status.Installed || status.Running || status.Detail == "" {
		t.Fatalf("missing service = %+v", status)
	}
}

func TestTerminalServiceUninstallOnlyOwnedFiles(t *testing.T) {
	s := terminalServiceFixture(t, "systemd")
	s.readFile = func(path string) ([]byte, error) {
		if path == terminalInstallMarker {
			return []byte("miproxy-panel-install-v1\nmanager=systemd\nbinary=/usr/local/bin/miproxy-panel\n"), nil
		}
		return []byte("ExecStart=/usr/local/bin/miproxy-panel\n"), nil
	}
	s.readlink = func(string) (string, error) { return s.binary, nil }
	var removed []string
	s.remove = func(path string) error { removed = append(removed, path); return nil }
	var commands []string
	s.run = func(_ context.Context, name string, args ...string) (string, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return "", nil
	}
	if _, err := s.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(removed, []string{s.servicePath, "/usr/local/bin/miproxy", s.binary, terminalInstallMarker}) {
		t.Fatalf("removed files = %v", removed)
	}
	if !reflect.DeepEqual(commands, []string{"systemctl disable miproxy-panel.service", "systemctl stop miproxy-panel.service", "systemctl daemon-reload"}) {
		t.Fatalf("uninstall commands = %v", commands)
	}
	removed, commands = nil, nil
	s.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	if _, err := s.Uninstall(context.Background()); err == nil || len(removed) != 0 || len(commands) != 0 {
		t.Fatal("unrecorded installation was changed")
	}
}

func TestTerminalServiceFailedStopDoesNotRemoveFiles(t *testing.T) {
	s := terminalServiceFixture(t, "procd")
	s.readFile = func(path string) ([]byte, error) {
		if path == terminalInstallMarker {
			return []byte("miproxy-panel-install-v1\nmanager=procd\nbinary=/usr/bin/miproxy-panel\n"), nil
		}
		return []byte("procd_set_param command /usr/bin/miproxy-panel"), nil
	}
	s.readlink = func(string) (string, error) { return s.binary, nil }
	s.remove = func(path string) error { t.Fatalf("removed %s after failed stop", path); return nil }
	s.run = func(_ context.Context, _ string, args ...string) (string, error) {
		if args[0] == "stop" {
			return "stop failed", errors.New("exit 1")
		}
		return "", nil
	}
	if _, err := s.Uninstall(context.Background()); err == nil || !strings.Contains(err.Error(), "stop failed") {
		t.Fatalf("uninstall error = %v", err)
	}
}

func TestTerminalCommandOutputIsBounded(t *testing.T) {
	b := &terminalCommandOutput{}
	_, _ = b.Write([]byte(strings.Repeat("a", 60<<10)))
	_, _ = b.Write([]byte(strings.Repeat("b", 10<<10)))
	if len(b.data) != 64<<10 || !strings.HasSuffix(string(b.data), strings.Repeat("b", 10<<10)) {
		t.Fatal("output was not bounded at 64 KiB")
	}
}

func TestTerminalInstallerStagingPreservesConfiguration(t *testing.T) {
	for _, target := range []string{"systemd", "openwrt"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			binary := filepath.Join(t.TempDir(), "miproxy-panel")
			if err := os.WriteFile(binary, []byte("test binary\n"), 0755); err != nil {
				t.Fatal(err)
			}
			install := func() {
				t.Helper()
				cmd := exec.Command("sh", "../../deploy/install.sh", "--binary", binary, "--system", target, "--no-start")
				cmd.Env = append(os.Environ(), "DESTDIR="+root)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("staged install: %v\n%s", err, output)
				}
			}
			install()
			envPath := filepath.Join(root, "etc/miproxy-panel.env")
			if err := os.WriteFile(envPath, []byte("PANEL_LISTEN=127.0.0.1:18787\n"), 0600); err != nil {
				t.Fatal(err)
			}
			install()
			data, err := os.ReadFile(envPath)
			if err != nil || string(data) != "PANEL_LISTEN=127.0.0.1:18787\n" {
				t.Fatal("existing environment file was changed")
			}
			prefix := "/usr/local/bin/"
			if target == "openwrt" {
				prefix = "/usr/bin/"
			}
			link, err := os.Readlink(filepath.Join(root, prefix, "miproxy"))
			if err != nil || link != prefix+"miproxy-panel" {
				t.Fatalf("entry point = %q, %v", link, err)
			}
			info, err := os.Stat(envPath)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("environment permissions are not 0600")
			}
		})
	}
}
