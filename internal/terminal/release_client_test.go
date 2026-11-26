package terminal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func panelReleaseFixture(t *testing.T) (panelRelease, []byte) {
	t.Helper()
	binary := []byte("verified release binary")
	digest := sha256.Sum256(binary)
	release := panelRelease{Tag: "v1.2.3", Asset: panelReleaseAsset{Name: "miproxy-panel_linux_arm64", Size: int64(len(binary)), Digest: "sha256:" + hex.EncodeToString(digest[:])}, Checksum: panelReleaseAsset{Name: "checksums.txt"}}
	base := "https://github.com/" + panelRepository + "/releases/download/v1.2.3/"
	release.Asset.URL = base + release.Asset.Name
	release.Checksum.URL = base + "checksums.txt"
	return release, binary
}
func panelFixtureClient(t *testing.T, release panelRelease, binary []byte, fault string) *http.Client {
	t.Helper()
	return &http.Client{Transport: releaseTransport(func(r *http.Request) (*http.Response, error) {
		var body []byte
		status := 200
		switch r.URL.String() {
		case panelLatestReleaseURL:
			tag := release.Tag
			if fault == "invalid-tag" {
				tag = "v1.2.3-rc1"
			}
			assetURL := release.Asset.URL
			if fault == "wrong-repo" {
				assetURL = strings.Replace(assetURL, panelRepository, "someone/else", 1)
			}
			digest := release.Asset.Digest
			assets := []map[string]any{{"name": release.Asset.Name, "browser_download_url": assetURL, "size": release.Asset.Size, "digest": digest}, {"name": "checksums.txt", "browser_download_url": release.Checksum.URL, "size": 100}}
			if fault == "missing-checksum" {
				assets = assets[:1]
			}
			if fault == "missing-arch" {
				assets = assets[1:]
			}
			body, _ = json.Marshal(map[string]any{"tag_name": tag, "draft": fault == "draft", "prerelease": fault == "prerelease", "assets": assets})
		case release.Checksum.URL:
			digest := sha256.Sum256(binary)
			body = []byte(hex.EncodeToString(digest[:]) + "  " + release.Asset.Name + "\n")
			if fault == "checksum" {
				body = []byte(strings.Repeat("0", 64) + "  " + release.Asset.Name + "\n")
			}
			if fault == "duplicate-checksum" {
				body = append(body, body...)
			}
		case release.Asset.URL:
			body = append([]byte{}, binary...)
			if fault == "corrupt" {
				body[0] ^= 0xff
			}
			if fault == "truncated" {
				body = body[:len(body)-1]
			}
		default:
			t.Fatalf("unexpected external request: %s", r.URL)
		}
		if fault == "rate-limit" {
			status = 403
		}
		if fault == "no-release" {
			status = 404
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}
}
func TestPanelReleaseSelectsExactRepositoryAndArchitecture(t *testing.T) {
	release, binary := panelReleaseFixture(t)
	for _, fault := range []string{"", "invalid-tag", "wrong-repo", "missing-checksum", "missing-arch", "draft", "prerelease", "rate-limit", "no-release"} {
		t.Run(fault, func(t *testing.T) {
			got, err := checkPanelRelease(context.Background(), panelFixtureClient(t, release, binary, fault), release.Asset.Name)
			if fault == "" {
				if err != nil || got.Tag != release.Tag || got.Asset != release.Asset {
					t.Fatalf("release mismatch: %+v %v", got, err)
				}
			} else if err == nil {
				t.Fatal("invalid release accepted")
			}
		})
	}
}
func TestPanelReleaseDownloadChecksSHA256BeforeInstall(t *testing.T) {
	release, binary := panelReleaseFixture(t)
	for _, fault := range []string{"", "checksum", "duplicate-checksum", "corrupt", "truncated"} {
		t.Run(fault, func(t *testing.T) {
			dir := t.TempDir()
			var progress panelDownloadProgress
			path, err := downloadPanelRelease(context.Background(), panelFixtureClient(t, release, binary, fault), release, dir, func(p panelDownloadProgress) { progress = p })
			if fault == "" {
				if err != nil {
					t.Fatal(err)
				}
				content, _ := os.ReadFile(path)
				if !bytes.Equal(content, binary) || progress.Downloaded != release.Asset.Size {
					t.Fatal("download corrupted or missing progress")
				}
				info, _ := os.Stat(path)
				if info.Mode().Perm() != 0700 {
					t.Fatal("unsafe download permissions")
				}
			} else {
				if err == nil {
					t.Fatal("corrupt download accepted")
				}
				entries, _ := os.ReadDir(dir)
				if len(entries) != 0 {
					t.Fatal("failed download left partial binary")
				}
			}
		})
	}
}
func TestPanelReleasePlatformAndVersionComparison(t *testing.T) {
	for _, target := range []struct{ system, arch, arm, name string }{{"linux", "amd64", "7", "linux_amd64"}, {"linux", "arm64", "7", "linux_arm64"}, {"linux", "arm", "5", "linux_armv5"}, {"linux", "arm", "7", "linux_armv7"}, {"linux", "mipsle", "7", "linux_mipsle_softfloat"}, {"darwin", "arm64", "7", "darwin_arm64"}} {
		name, err := panelAssetName(target.system, target.arch, target.arm)
		if err != nil || name != "miproxy-panel_"+target.name {
			t.Fatalf("platform: %s %v", name, err)
		}
	}
	for _, pair := range []struct {
		next, current string
		newer         bool
	}{{"v1.10.0", "v1.9.0", true}, {"v1.2.3", "v1.2.3", false}, {"v1.2.3", "v2.0.0", false}, {"v1.2.3", "dev", true}, {"v1.2.3", "1.2.2", true}} {
		got, err := panelReleaseNewer(pair.next, pair.current)
		if err != nil || got != pair.newer {
			t.Fatalf("version comparison %+v: %v %v", pair, got, err)
		}
	}
	if _, err := panelAssetName("linux", "arm", "8"); err == nil {
		t.Fatal("unsupported ARM accepted")
	}
}
func TestTerminalPanelUpdateUsesGitHubCheckAndVersionConfirmation(t *testing.T) {
	m := terminalTestModel(t)
	m.page = 6
	m.nav = false
	next, cmd := terminalKey(m, "enter")
	if cmd == nil || !next.busy || next.form != nil {
		t.Fatal("update must check GitHub, not ask for a local binary")
	}
	release, _ := panelReleaseFixture(t)
	model, _ := m.Update(terminalReleaseResult{release: release})
	got := model.(terminalModel)
	if got.confirmation == nil || !strings.Contains(got.confirmation.text, release.Tag) {
		t.Fatal("version was not shown before updating")
	}
	model, _ = m.Update(terminalReleaseResult{err: fmt.Errorf("GitHub 请求被限制")})
	got = model.(terminalModel)
	if !got.failed || got.busy || got.confirmation != nil {
		t.Fatal("check failure did not restore interface")
	}
	events := make(chan tea.Msg, 1)
	events <- terminalActionResult{text: "done"}
	close(events)
	if terminalWaitPanelUpdate(events)().(terminalActionResult).text != "done" {
		t.Fatal("update completion lost")
	}
}
func TestPanelUpdateRejectsVersionMismatchBeforeStoppingService(t *testing.T) {
	s := terminalServiceFixture(t, "systemd")
	dir := t.TempDir()
	s.binary = filepath.Join(dir, "miproxy-panel")
	os.WriteFile(s.binary, []byte("old"), 0755)
	candidate := filepath.Join(dir, "new")
	os.WriteFile(candidate, []byte("new"), 0755)
	s.readFile = func(string) ([]byte, error) {
		return []byte("miproxy-panel-install-v1\nmanager=systemd\nbinary=" + s.binary + "\n"), nil
	}
	s.run = func(_ context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" {
			t.Fatal("service changed before binary version verified")
		}
		return "MiProxy Panel v9.0.0", nil
	}
	if _, err := s.updateBinary(context.Background(), candidate, "v1.2.3"); err == nil {
		t.Fatal("mismatched release binary accepted")
	}
	content, _ := os.ReadFile(s.binary)
	if string(content) != "old" {
		t.Fatal("current binary replaced")
	}
}

func TestPanelReleaseRedirectsStayOnGitHub(t *testing.T) {
	client := newPanelReleaseClient()
	defer client.CloseIdleConnections()
	for _, address := range []string{"https://release-assets.githubusercontent.com/file", "https://objects.githubusercontent.com/file", "https://github.com/" + panelRepository + "/releases/download/v1.2.3/file"} {
		request, _ := http.NewRequest("GET", address, nil)
		if err := client.CheckRedirect(request, []*http.Request{{}}); err != nil {
			t.Fatalf("valid GitHub redirect refused: %v", err)
		}
	}
	for _, address := range []string{"http://github.com/file", "https://untrusted.example/file"} {
		request, _ := http.NewRequest("GET", address, nil)
		if err := client.CheckRedirect(request, nil); err == nil {
			t.Fatal("untrusted redirect accepted")
		}
	}
}

// releaseTransport keeps download fixtures independent of the server package.
type releaseTransport func(*http.Request) (*http.Response, error)

func (transport releaseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}
