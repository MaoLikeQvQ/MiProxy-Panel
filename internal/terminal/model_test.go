package terminal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/safeoutput"
)

func terminalTestModel(t *testing.T) terminalModel {
	t.Helper()
	t.Setenv("PANEL_LISTEN", "127.0.0.1:8787")
	return newTerminalModel(context.Background(), newTerminalClient("/missing/test.sock"), "/tmp/config.yaml")
}
func terminalKey(m terminalModel, key string) (terminalModel, tea.Cmd) {
	var k tea.KeyMsg
	switch key {
	case "enter":
		k.Type = tea.KeyEnter
	case "esc":
		k.Type = tea.KeyEsc
	case "up":
		k.Type = tea.KeyUp
	case "down":
		k.Type = tea.KeyDown
	case "tab":
		k.Type = tea.KeyTab
	default:
		k = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	next, cmd := m.Update(k)
	return next.(terminalModel), cmd
}
func TestTerminalNavigationAndConfirmation(t *testing.T) {
	m := terminalTestModel(t)
	m, _ = terminalKey(m, "3")
	if m.page != 2 || m.nav {
		t.Fatal("subscription shortcut did not enter content")
	}
	m.data.subscriptions = []subscriptionInfo{{ID: "test", Name: "示例订阅", Kind: "url"}}
	m, _ = terminalKey(m, "enter")
	if m.confirmation == nil || m.busy {
		t.Fatal("applying subscription must require explicit confirmation")
	}
	m, _ = terminalKey(m, "enter")
	if m.confirmation == nil {
		t.Fatal("Enter must not execute destructive confirmation")
	}
	m, _ = terminalKey(m, "n")
	if m.confirmation != nil || m.busy {
		t.Fatal("cancel changed operation state")
	}
	m, _ = terminalKey(m, "esc")
	if !m.nav {
		t.Fatal("Escape did not restore menu")
	}
	m, _ = terminalKey(m, "7")
	if m.page != 6 {
		t.Fatal("update shortcut broken")
	}
	m, _ = terminalKey(m, "8")
	if m.page != 6 {
		t.Fatal("invalid menu number changed page")
	}
}
func TestTerminalFormTreatsShortcutsAsInputAndMasksLinks(t *testing.T) {
	m := terminalTestModel(t)
	m.page = 2
	m.nav = false
	m, _ = terminalKey(m, "a")
	m, _ = terminalKey(m, "测试订阅")
	m, _ = terminalKey(m, "enter")
	if m.form == nil || m.form.kind != "url" {
		t.Fatal("name did not advance to link input")
	}
	const token = "https://private.example/api/secret-token?q=private"
	m, _ = terminalKey(m, token)
	if strings.Contains(ansi.Strip(m.View()), "secret-token") {
		t.Fatal("secret subscription URL exposed in terminal")
	}
	m, _ = terminalKey(m, "q")
	if m.form == nil || !strings.HasSuffix(m.form.value, "q") {
		t.Fatal("q must be editable inside form")
	}
	m, _ = terminalKey(m, "esc")
	if m.form != nil {
		t.Fatal("form did not cancel")
	}
}
func TestTerminalViewFitsResizedWindowsAndHasNoRemovedMenus(t *testing.T) {
	m := terminalTestModel(t)
	m.data.subscriptions = []subscriptionInfo{{ID: "one", Name: strings.Repeat("中文长名称", 100)}}
	for _, size := range [][2]int{{112, 29}, {80, 24}, {40, 16}, {150, 45}} {
		for page := range terminalMenus {
			m.width, m.height, m.page = size[0], size[1], page
			for _, plain := range []bool{false, true} {
				m.noColor = plain
				view := m.View()
				lines := strings.Split(view, "\n")
				if len(lines) != m.height {
					t.Fatalf("%v page %d rows=%d", size, page, len(lines))
				}
				for _, line := range lines {
					if ansi.StringWidth(line) > m.width {
						t.Fatalf("%v page %d line width=%d", size, page, ansi.StringWidth(line))
					}
				}
				if strings.Contains(view, "登录") || strings.Contains(view, "备份") || strings.Contains(view, "面板设置") {
					t.Fatal("removed menu reappeared")
				}
				if plain && strings.Contains(view, "\x1b") {
					t.Fatal("NO_COLOR contains ANSI")
				}
			}
		}
	}
}
func TestTerminalEnvironmentDoesNotExecuteShellOrReplaceProcessValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "panel.env")
	marker := filepath.Join(dir, "must-not-exist")
	t.Setenv("PANEL_LISTEN", "127.0.0.1:19000")
	t.Setenv("PANEL_CONFIG", "")
	os.Unsetenv("PANEL_CONFIG")
	t.Cleanup(func() { os.Unsetenv("PANEL_CONFIG") })
	content := "PANEL_LISTEN=0.0.0.0:1\nPANEL_CONFIG='$(touch " + marker + ")'\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadTerminalEnvironment(path, true); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PANEL_LISTEN") != "127.0.0.1:19000" || os.Getenv("PANEL_CONFIG") != "$(touch "+marker+")" {
		t.Fatal("deployment env values changed")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("environment file executed shell")
	}
}
func TestTerminalRejectsTerminalEscapesAndHidesURLSecrets(t *testing.T) {
	text := safeoutput.Text("\x1b[2Jhttps://user:password@example.com/private-token?secret=abc\nname\x00")
	for _, forbidden := range []string{"\x1b", "password", "private-token", "secret=abc", "\x00", "\n"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("unsafe output: %q", text)
		}
	}
}
func TestTerminalPollDoesNotReplaceCurrentPageWithStaleResult(t *testing.T) {
	m := terminalTestModel(t)
	m.page = 3
	m.polling = true
	next, cmd := m.Update(terminalData{page: 2, online: true, subscriptions: []subscriptionInfo{{Name: "stale"}}})
	got := next.(terminalModel)
	if got.page != 3 || len(got.data.subscriptions) != 0 || cmd == nil {
		t.Fatal("stale response replaced page or did not trigger current refresh")
	}
}
func TestTerminalDoesNotStartLogsWithoutConnectedCore(t *testing.T) {
	m := terminalTestModel(t)
	m.page = 5
	next, cmd := m.Update(terminalData{page: 5, online: true})
	if next.(terminalModel).logs != nil || cmd != nil {
		t.Fatal("logs opened before core connection")
	}
}
func TestTerminalUpdateRejectsUnownedOrInvalidBinary(t *testing.T) {
	s := terminalServiceFixture(t, "systemd")
	s.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	if _, err := s.updateBinary(context.Background(), "/tmp/binary", ""); err == nil {
		t.Fatal("unowned update accepted")
	}
	s.readFile = func(string) ([]byte, error) {
		return []byte("miproxy-panel-install-v1\nmanager=systemd\nbinary=/usr/local/bin/miproxy-panel\n"), nil
	}
	if _, err := s.updateBinary(context.Background(), "relative", ""); err == nil {
		t.Fatal("relative binary accepted")
	}
}
func TestTerminalContextCancelsOfflineRequests(t *testing.T) {
	client := newTerminalClient("/missing/test.sock")
	defer client.http.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := client.request(ctx, "GET", "/api/core", nil, &coreSnapshot{}); err == nil || time.Since(start) > time.Second {
		t.Fatal("cancelled request did not stop")
	}
}
func TestTerminalOfflineNormalEntryStillRendersMenu(t *testing.T) {
	m := terminalTestModel(t)
	d := m.fetch()().(terminalData)
	next, _ := m.Update(d)
	view := ansi.Strip(next.(terminalModel).View())
	if d.online || !strings.Contains(view, "服务管理") || !strings.Contains(view, "未连接") {
		t.Fatal("offline menu unavailable")
	}
}
func TestTerminalClientExternalConfigCannotApply(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "miproxy-terminal-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "panel.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	handler := http.NewServeMux()
	handler.HandleFunc("POST /api/subscriptions/{id}/apply", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"当前配置由外部应用管理，请在原应用中切换订阅。"}`))
	})
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	client := newTerminalClient(path)
	defer client.http.CloseIdleConnections()
	if _, err := client.switchSubscription(context.Background(), "test"); err == nil || !strings.Contains(err.Error(), "外部应用管理") {
		t.Fatalf("external config mutation did not return server refusal: %v", err)
	}
}

func TestTerminalSubscriptionActionsVisibleAndAvailableFromMenu(t *testing.T) {
	m := terminalTestModel(t)
	m.page = 2
	m.nav = true
	view := ansi.Strip(m.View())
	for _, label := range []string{"导入订阅", "删除订阅", "切换应用"} {
		if !strings.Contains(view, label) {
			t.Fatalf("missing visible subscription action: %s", label)
		}
	}
	m, _ = terminalKey(m, "a")
	if m.form == nil || m.nav {
		t.Fatal("import shortcut inaccessible while subscription menu focused")
	}
}

func TestTerminalSubscriptionActionsStayVisibleWhenListScrolls(t *testing.T) {
	m := terminalTestModel(t)
	m.width, m.height, m.page, m.selected, m.nav = 80, 24, 2, 11, false
	for i := 0; i < 12; i++ {
		m.data.subscriptions = append(m.data.subscriptions, subscriptionInfo{ID: fmt.Sprint(i), Name: fmt.Sprintf("订阅%02d", i)})
	}
	view := ansi.Strip(m.View())
	for _, label := range []string{"导入订阅", "删除订阅", "› 订阅11"} {
		if !strings.Contains(view, label) {
			t.Errorf("scrolled subscription view lost %q", label)
		}
	}
}

func TestTerminalWideFormShowsInputTailAndWholeConfirmation(t *testing.T) {
	m := terminalTestModel(t)
	m.width, m.height, m.page = 117, 46, 2
	m.form = &terminalForm{title: "导入订阅", label: "订阅名称", value: "我的订阅"}
	if !strings.Contains(ansi.Strip(m.View()), "我的订阅▏") {
		t.Error("short input is hidden")
	}
	m.form = &terminalForm{title: "导入本地 YAML", label: "文件路径", value: "/very/long/" + strings.Repeat("directory/", 50) + "config.yaml"}
	if !strings.Contains(ansi.Strip(m.View()), "config.yaml▏") {
		t.Error("right-column form truncates input tail/caret")
	}
	m.form = nil
	m.confirmation = &terminalConfirmation{text: strings.Repeat("保留配置并切换订阅", 8) + "确认说明结束"}
	if !strings.Contains(ansi.Strip(m.View()), "确认说明结束") {
		t.Error("right-column confirmation loses wrapped text")
	}
}

func TestTerminalResizeRepaintsAndClearWorksInsideForms(t *testing.T) {
	m := terminalTestModel(t)
	m.form = &terminalForm{kind: "url-name", value: "保留已输入内容"}
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 117, Height: 46})
	m = next.(terminalModel)
	if cmd == nil || m.width != 117 || m.height != 46 || m.form.value != "保留已输入内容" {
		t.Fatal("resize must repaint and preserve input")
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	if cmd == nil || next.(terminalModel).form.value != "保留已输入内容" {
		t.Fatal("clear inside form must repaint without clearing input")
	}
	next, cmd = m.Update(tea.WindowSizeMsg{})
	if next.(terminalModel).width != 117 || cmd != nil {
		t.Fatal("zero-size resize event must not replace valid dimensions")
	}
}

func TestTerminalServiceMenuTogglesWithStatus(t *testing.T) {
	for _, running := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("running=%v/enabled=%v", running, enabled), func(t *testing.T) {
				m := terminalTestModel(t)
				m.page = 1
				m.nav = false
				m.data.service = terminalServiceStatus{Installed: true, Running: running, Enabled: enabled, Manager: "systemd"}
				m.service = terminalServiceFixture(t, "systemd")
				var actions []string
				m.service.run = func(_ context.Context, _ string, args ...string) (string, error) {
					actions = append(actions, args[0])
					return "", nil
				}
				options := m.serviceOptions()
				wantAction := []string{terminalYes(running, "stop", "start"), "restart", terminalYes(enabled, "disable", "enable")}
				wantLabel := []string{terminalYes(running, "停止面板服务", "启动面板服务"), "重启面板服务", terminalYes(enabled, "关闭开机启动", "开启开机启动")}
				if m.itemCount() != 3 {
					t.Fatal("service toggles should occupy three rows")
				}
				view := ansi.Strip(m.View())
				for i, option := range options {
					if option.action != wantAction[i] || option.label != wantLabel[i] || !strings.Contains(view, wantLabel[i]) {
						t.Fatalf("option %d does not match status: %+v", i, option)
					}
					current := m
					current.selected = i
					current, _ = terminalKey(current, "enter")
					if current.confirmation == nil {
						t.Fatal("service operation lost confirmation")
					}
					_, cmd := terminalKey(current, "y")
					if cmd == nil {
						t.Fatal("confirmed service operation did not execute")
					}
					result := cmd().(terminalActionResult)
					if result.err != nil || actions[len(actions)-1] != wantAction[i] {
						t.Fatalf("wrong service command: %v %v", actions, result.err)
					}
				}
				for _, hidden := range []string{terminalYes(running, "启动面板服务", "停止面板服务"), terminalYes(enabled, "开启开机启动", "关闭开机启动")} {
					if strings.Contains(view, hidden) {
						t.Fatalf("opposite action is also visible: %s", hidden)
					}
				}
				// The next status poll flips both labels without changing the selection.
				m.selected = 2
				next, _ := m.Update(terminalData{page: 1, service: terminalServiceStatus{Installed: true, Running: !running, Enabled: !enabled}})
				updated := next.(terminalModel)
				if updated.selected != 2 || updated.serviceOptions()[0].action == wantAction[0] || updated.serviceOptions()[2].action == wantAction[2] {
					t.Fatal("status refresh did not flip toggle labels")
				}
			})
		}
	}
}

func TestTerminalDisplayClearShortcutAndInitialSize(t *testing.T) {
	m := terminalTestModel(t)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	if cmd == nil || next.(terminalModel).page != m.page {
		t.Fatal("redraw shortcut not available")
	}
}

func TestTerminalEnvironmentIgnoresRemovedPasswordVariables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.env")
	for _, key := range []string{"PANEL_PASSWORD", "MIHOMO_SECRET"} {
		t.Setenv(key, "")
		_ = os.Unsetenv(key)
	}
	if err := os.WriteFile(path, []byte(`PANEL_PASSWORD='obsolete'
MIHOMO_SECRET=obsolete
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadTerminalEnvironment(path, true); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"PANEL_PASSWORD", "MIHOMO_SECRET"} {
		if _, found := os.LookupEnv(key); found {
			t.Fatalf("removed credential variable %s was loaded", key)
		}
	}
}

func TestConfigureAccessRequiresInteractiveTerminal(t *testing.T) {
	if err := ConfigureAccess(); err == nil {
		t.Fatal("non-interactive access setting accepted")
	}
}
