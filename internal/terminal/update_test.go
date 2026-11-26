package terminal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalUpdateReplacesBinaryAndRollsBackFailedStart(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(terminalYes(fail, "rollback", "success"), func(t *testing.T) {
			dir := t.TempDir()
			s := terminalServiceFixture(t, "systemd")
			s.binary = filepath.Join(dir, "miproxy-panel")
			s.servicePath = filepath.Join(dir, "service")
			if err := os.WriteFile(s.binary, []byte("old"), 0755); err != nil {
				t.Fatal(err)
			}
			candidate := filepath.Join(dir, "new")
			if err := os.WriteFile(candidate, []byte("new"), 0755); err != nil {
				t.Fatal(err)
			}
			s.readFile = func(path string) ([]byte, error) {
				if path == terminalInstallMarker {
					return []byte("miproxy-panel-install-v1\nmanager=systemd\nbinary=" + s.binary + "\n"), nil
				}
				return os.ReadFile(path)
			}
			starts := 0
			s.run = func(_ context.Context, name string, args ...string) (string, error) {
				if strings.Contains(name, ".miproxy-update-") {
					return "MiProxy Panel v-test", nil
				}
				switch args[0] {
				case "is-active":
					return "active", nil
				case "is-enabled":
					return "enabled", nil
				case "start":
					starts++
					if fail && starts == 1 {
						return "start failed", errors.New("start failed")
					}
				}
				return "", nil
			}
			_, err := s.updateBinary(context.Background(), candidate, "v-test")
			got, _ := os.ReadFile(s.binary)
			if fail {
				if err == nil || string(got) != "old" || starts != 2 {
					t.Fatalf("rollback failed: %q %v starts=%d", got, err, starts)
				}
			} else if err != nil || string(got) != "new" || starts != 1 {
				t.Fatalf("update failed: %q %v starts=%d", got, err, starts)
			}
			files, _ := filepath.Glob(filepath.Join(dir, ".miproxy-update-*"))
			if len(files) != 0 {
				t.Fatal("temporary binaries leaked")
			}
		})
	}
}
