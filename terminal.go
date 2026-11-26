package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
)

var terminalMenus = []string{"运行概览", "服务管理", "订阅管理", "代理组与节点", "Mihomo 内核", "日志与诊断", "更新与卸载"}

type terminalData struct {
	online        bool
	core          coreSnapshot
	overview      runtimeOverview
	service       terminalServiceStatus
	subscriptions []subscriptionInfo
	groups        []runtimeGroup
	err, pageErr  string
	page          int
}
type terminalReleaseResult struct {
	release panelRelease
	err     error
}
type terminalPanelUpdate struct{ events <-chan tea.Msg }
type terminalTick time.Time
type terminalActionResult struct {
	text string
	err  error
	quit bool
}
type terminalDiscovery struct {
	Candidates []coreCandidate
	Warnings   []string
}
type terminalDiscoveryResult struct {
	data terminalDiscovery
	err  error
}
type terminalLogs struct {
	lines      <-chan string
	cancel     context.CancelFunc
	generation int
}
type terminalLogLine struct {
	text       string
	generation int
	closed     bool
}
type terminalTextResult struct {
	title, text string
	err         error
}
type terminalDelayResult struct {
	delays map[string]string
	err    error
}
type terminalForm struct {
	kind, title, label, value, name string
	secret                          bool
}
type terminalConfirmation struct {
	text string
	cmd  tea.Cmd
}
type terminalServiceOption struct {
	label, action, confirmation string
}

type terminalModel struct {
	ctx                                   context.Context
	client                                *terminalClient
	service                               terminalService
	configPath, address                   string
	width, height, page, selected, scroll int
	nav, noColor, polling, busy           bool
	group                                 string
	data                                  terminalData
	notice                                string
	failed                                bool
	form                                  *terminalForm
	confirmation                          *terminalConfirmation
	discovery                             terminalDiscovery
	delays                                map[string]string
	logLines                              []string
	logs                                  *terminalLogs
	logGeneration                         int
	textTitle                             string
	textLines                             []string
	textOffset                            int
	panelRelease                          *panelRelease
	panelUpdateEvents                     <-chan tea.Msg
}

func runTerminal() error {
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return errors.New("miproxy 需要交互终端；请在 SSH 或本机终端运行，查看用法可执行 miproxy --help")
	}
	path := env("MIPROXY_ENV_FILE", "/etc/miproxy-panel.env")
	if err := loadTerminalEnvironment(path, os.Getenv("MIPROXY_ENV_FILE") != ""); err != nil {
		return err
	}
	configPath, err := terminalConfigPath()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	client := newTerminalClient(localControlPath(configPath))
	defer client.http.CloseIdleConnections()
	m := newTerminalModel(ctx, client, configPath)
	if width, height, sizeErr := term.GetSize(os.Stdout.Fd()); sizeErr == nil && width > 0 && height > 0 {
		m.width = width
		m.height = height
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return err
}

func newTerminalModel(ctx context.Context, client *terminalClient, configPath string) terminalModel {
	return terminalModel{
		ctx: ctx, client: client, service: newTerminalService(),
		configPath: configPath, address: env("PANEL_LISTEN", "127.0.0.1:8787"),
		width: 100, height: 32, nav: true, polling: true,
		noColor: os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb",
		delays:  map[string]string{},
	}
}
func terminalNextTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return terminalTick(t) })
}
func (m terminalModel) Init() tea.Cmd { return tea.Batch(m.fetch(), terminalNextTick()) }
func (m terminalModel) fetch() tea.Cmd {
	page, client, service, parent := m.page, m.client, m.service, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 8*time.Second)
		defer cancel()
		d := terminalData{page: page}
		var wg sync.WaitGroup
		wg.Go(func() { d.service = service.Status(ctx) })
		wg.Go(func() {
			if err := client.request(ctx, http.MethodGet, "/api/core", nil, &d.core); err != nil {
				d.err = err.Error()
			} else {
				d.online = true
			}
		})
		wg.Go(func() { _ = client.request(ctx, http.MethodGet, "/api/overview", nil, &d.overview) })
		if page == 2 {
			wg.Go(func() {
				if err := client.request(ctx, http.MethodGet, "/api/subscriptions", nil, &d.subscriptions); err != nil {
					d.pageErr = err.Error()
				}
			})
		}
		if page == 3 {
			wg.Go(func() {
				if err := client.request(ctx, http.MethodGet, "/api/groups", nil, &d.groups); err != nil {
					d.pageErr = err.Error()
				} else {
					sort.Slice(d.groups, func(i, j int) bool { return d.groups[i].Name < d.groups[j].Name })
				}
			})
		}
		wg.Wait()
		return d
	}
}
func (m *terminalModel) refresh() tea.Cmd {
	if m.polling {
		return nil
	}
	m.polling = true
	return m.fetch()
}
func (m *terminalModel) open(page int) tea.Cmd {
	m.stopLogs()
	m.page = page
	m.selected = 0
	m.scroll = 0
	m.group = ""
	m.form = nil
	m.confirmation = nil
	m.textTitle = ""
	m.notice = ""
	m.failed = false
	m.data.pageErr = ""
	m.data.subscriptions = nil
	m.data.groups = nil
	return m.refresh()
}
func (m *terminalModel) stopLogs() {
	if m.logs != nil {
		m.logs.cancel()
		m.logs = nil
	}
	m.logGeneration++
}
func (m terminalModel) startLogs() tea.Cmd {
	client, parent, generation := m.client, m.ctx, m.logGeneration
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(parent)
		lines := make(chan string, 200)
		go func() {
			defer close(lines)
			err := client.logs(ctx, func(line string) {
				select {
				case lines <- line:
				case <-ctx.Done():
				}
			})
			if err != nil && ctx.Err() == nil {
				select {
				case lines <- err.Error():
				case <-ctx.Done():
				}
			}
		}()
		return terminalLogs{lines: lines, cancel: cancel, generation: generation}
	}
}
func terminalWaitLog(logs terminalLogs) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-logs.lines
		return terminalLogLine{text: line, generation: logs.generation, closed: !ok}
	}
}
func (m *terminalModel) action(cmd tea.Cmd) tea.Cmd {
	if m.busy {
		return nil
	}
	m.busy = true
	m.failed = false
	m.notice = "正在执行，请稍候…"
	return cmd
}
func (m terminalModel) apiAction(method, path string, payload any, success string) tea.Cmd {
	client, parent := m.client, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
		defer cancel()
		err := client.request(ctx, method, path, payload, nil)
		return terminalActionResult{text: success, err: err}
	}
}
func (m terminalModel) serviceAction(action string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 45*time.Second)
		defer cancel()
		text, err := m.service.Action(ctx, action)
		return terminalActionResult{text: text, err: err}
	}
}
func (m *terminalModel) confirm(text string, cmd tea.Cmd) {
	if !m.busy {
		m.confirmation = &terminalConfirmation{text: text, cmd: cmd}
	}
}

func (m terminalModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width <= 0 || msg.Height <= 0 {
			return m, nil
		}
		changed := m.width != msg.Width || m.height != msg.Height
		m.width = msg.Width
		m.height = msg.Height
		if changed {
			return m, tea.ClearScreen
		}
	case terminalTick:
		cmd := m.refresh()
		return m, tea.Batch(cmd, terminalNextTick())
	case terminalData:
		m.polling = false
		if msg.page != m.page {
			m.data.online = msg.online
			m.data.core = msg.core
			m.data.overview = msg.overview
			m.data.service = msg.service
			m.data.err = msg.err
			cmd := m.refresh()
			return m, cmd
		}
		m.data = msg
		m.selected = min(m.selected, max(0, m.itemCount()-1))
		if m.page == 5 && m.data.core.Connected && m.logs == nil {
			m.logGeneration++
			return m, m.startLogs()
		}
		if m.logs != nil && !m.data.core.Connected {
			m.stopLogs()
		}
	case terminalActionResult:
		m.panelUpdateEvents = nil
		m.busy = false
		m.failed = msg.err != nil
		if msg.err != nil {
			m.notice = terminalSafeText(msg.err.Error())
		} else {
			m.notice = terminalSafeText(msg.text)
		}
		if msg.quit && msg.err == nil {
			m.stopLogs()
			return m, tea.Quit
		}
		cmd := m.refresh()
		return m, cmd
	case terminalReleaseResult:
		m.busy = false
		if msg.err != nil {
			m.notice = terminalSafeText(msg.err.Error())
			m.failed = true
			return m, nil
		}
		m.panelRelease = &msg.release
		newer, err := panelReleaseNewer(msg.release.Tag, version)
		if err != nil {
			m.notice = err.Error()
			m.failed = true
			return m, nil
		}
		if !newer {
			m.notice = "当前已是最新版本（" + version + "）。"
			return m, nil
		}
		m.notice = "发现新版本 " + msg.release.Tag
		m.confirm("从 GitHub 更新面板 "+version+" → "+msg.release.Tag+"（"+terminalBytes(float64(msg.release.Asset.Size))+"）？校验通过后替换程序，正在运行的服务和自有内核会重启。", m.downloadPanelUpdate(msg.release))
	case terminalPanelUpdate:
		m.panelUpdateEvents = msg.events
		return m, terminalWaitPanelUpdate(msg.events)
	case panelDownloadProgress:
		ratio := float64(msg.Downloaded) / float64(max(1, msg.Total))
		m.notice = fmt.Sprintf("正在从 GitHub 下载面板：%.0f%%  %s / %s", ratio*100, terminalBytes(float64(msg.Downloaded)), terminalBytes(float64(msg.Total)))
		if m.panelUpdateEvents != nil {
			return m, terminalWaitPanelUpdate(m.panelUpdateEvents)
		}
	case terminalDiscoveryResult:
		m.busy = false
		if msg.err != nil {
			m.notice = terminalSafeText(msg.err.Error())
			m.failed = true
		} else {
			m.discovery = msg.data
			m.selected = 0
			m.notice = fmt.Sprintf("检测完成：找到 %d 个候选。", len(msg.data.Candidates))
			m.failed = false
		}
	case terminalDelayResult:
		m.busy = false
		for name, delay := range msg.delays {
			m.delays[name] = delay
		}
		if msg.err != nil {
			m.failed = true
			m.notice = msg.err.Error()
		} else {
			m.notice = "本组测速完成。"
		}
	case terminalTextResult:
		m.busy = false
		if msg.err != nil {
			m.failed = true
			m.notice = terminalSafeText(msg.err.Error())
		} else {
			m.textTitle = msg.title
			m.textLines = nil
			m.textOffset = 0
			for _, line := range strings.Split(msg.text, "\n") {
				m.textLines = append(m.textLines, terminalSafeText(line))
			}
		}
	case terminalLogs:
		if m.page != 5 || msg.generation != m.logGeneration {
			msg.cancel()
			return m, nil
		}
		m.logs = &msg
		return m, terminalWaitLog(msg)
	case terminalLogLine:
		if m.logs == nil || msg.generation != m.logGeneration {
			return m, nil
		}
		if msg.closed {
			m.logs.cancel()
			m.logs = nil
			return m, nil
		}
		m.logLines = append(m.logLines, msg.text)
		if len(m.logLines) > 200 {
			m.logLines = m.logLines[len(m.logLines)-200:]
		}
		return m, terminalWaitLog(*m.logs)
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" {
			m.stopLogs()
			return m, tea.Quit
		}
		if key == "ctrl+l" {
			return m, tea.ClearScreen
		}
		if m.form != nil {
			cmd := m.formKey(msg)
			return m, cmd
		}
		if m.confirmation != nil {
			if key == "esc" || key == "n" {
				m.confirmation = nil
			} else if key == "y" {
				cmd := m.confirmation.cmd
				m.confirmation = nil
				action := m.action(cmd)
				return m, action
			}
			return m, nil
		}
		if m.textTitle != "" {
			switch key {
			case "esc", "q":
				m.textTitle = ""
			case "up", "k":
				m.textOffset = max(0, m.textOffset-1)
			case "down", "j":
				m.textOffset = min(max(0, len(m.textLines)-1), m.textOffset+1)
			case "pgdown":
				m.textOffset = min(max(0, len(m.textLines)-1), m.textOffset+max(1, m.height-9))
			case "pgup":
				m.textOffset = max(0, m.textOffset-max(1, m.height-9))
			}
			return m, nil
		}
		if key == "q" || key == "0" {
			m.stopLogs()
			return m, tea.Quit
		}
		if key == "esc" {
			if m.group != "" {
				m.group = ""
				m.selected = 0
			} else {
				m.nav = true
			}
			return m, nil
		}
		if key == "tab" || key == "left" || key == "right" {
			m.nav = !m.nav
			return m, nil
		}
		if key == "r" {
			cmd := m.refresh()
			return m, cmd
		}
		if m.busy {
			return m, nil
		}
		if len(key) == 1 && key[0] >= '1' && key[0] <= '7' {
			n := int(key[0] - '1')
			cmd := m.open(n)
			m.nav = false
			return m, cmd
		}
		if m.page == 2 && (key == "a" || key == "f" || key == "d" || key == "delete" || key == "s" || key == "u") {
			m.nav = false
			cmd := m.pageKey(key)
			return m, cmd
		}
		if m.nav {
			switch key {
			case "up", "k":
				cmd := m.open((m.page + len(terminalMenus) - 1) % len(terminalMenus))
				return m, cmd
			case "down", "j":
				cmd := m.open((m.page + 1) % len(terminalMenus))
				return m, cmd
			case "enter":
				m.nav = false
			}
			return m, nil
		}
		switch key {
		case "up", "k":
			m.selected = max(0, m.selected-1)
			m.scroll = max(0, m.scroll-1)
			return m, nil
		case "down", "j":
			m.selected = min(max(0, m.itemCount()-1), m.selected+1)
			m.scroll++
			return m, nil
		case "pgdown":
			m.scroll += max(1, m.height-10)
			return m, nil
		case "pgup":
			m.scroll = max(0, m.scroll-max(1, m.height-10))
			return m, nil
		}
		cmd := m.pageKey(key)
		return m, cmd
	}
	return m, nil
}

func (m terminalModel) itemCount() int {
	switch m.page {
	case 1:
		return len(m.serviceOptions())
	case 2:
		return len(m.data.subscriptions)
	case 3:
		if g := m.currentGroup(); g != nil {
			return len(g.All)
		}
		return len(m.data.groups)
	case 4:
		return len(m.discovery.Candidates)
	case 6:
		return 3
	}
	return 0
}

func (m terminalModel) serviceOptions() []terminalServiceOption {
	running := m.data.service.Running
	if !m.data.service.Installed {
		running = m.data.online
	}
	toggle := terminalServiceOption{
		label: "启动面板服务", action: "start", confirmation: "启动面板服务？",
	}
	if running {
		toggle = terminalServiceOption{
			label: "停止面板服务", action: "stop",
			confirmation: "停止面板服务？面板启动的 Mihomo 也会停止，代理连接可能中断。",
		}
	}
	autoStart := terminalServiceOption{
		label: "开启开机启动", action: "enable", confirmation: "开启面板服务的开机启动？",
	}
	if m.data.service.Enabled {
		autoStart = terminalServiceOption{
			label: "关闭开机启动", action: "disable", confirmation: "关闭面板服务的开机启动？",
		}
	}
	return []terminalServiceOption{
		toggle,
		{label: "重启面板服务", action: "restart", confirmation: "重启面板服务？面板启动的 Mihomo 会随之重启，代理连接可能短暂中断。"},
		autoStart,
	}
}
func (m terminalModel) currentGroup() *runtimeGroup {
	for i := range m.data.groups {
		if m.data.groups[i].Name == m.group {
			return &m.data.groups[i]
		}
	}
	return nil
}
func (m terminalModel) selectedSubscription() *subscriptionInfo {
	if m.selected >= 0 && m.selected < len(m.data.subscriptions) {
		return &m.data.subscriptions[m.selected]
	}
	return nil
}

func (m *terminalModel) pageKey(key string) tea.Cmd {
	switch m.page {
	case 0:
		if key == "enter" {
			return m.refresh()
		}
	case 1:
		if key == "enter" {
			options := m.serviceOptions()
			if m.selected >= 0 && m.selected < len(options) {
				option := options[m.selected]
				m.confirm(option.confirmation, m.serviceAction(option.action))
			}
		}
	case 2:
		switch key {
		case "a":
			m.form = &terminalForm{kind: "url-name", title: "添加订阅", label: "订阅名称"}
		case "f":
			m.form = &terminalForm{kind: "file-name", title: "导入本地 YAML", label: "订阅名称"}
		case "enter", "s", "u", "d", "delete":
			sub := m.selectedSubscription()
			if sub == nil {
				m.notice = "还没有订阅，按 a 添加链接或 f 导入文件。"
				return nil
			}
			id, name := sub.ID, terminalSafeText(sub.Name)
			path := "/api/subscriptions/" + url.PathEscape(id)
			if key == "enter" || key == "s" {
				m.confirm("切换应用“"+name+"”？将替换当前代理与分流配置，并叠加已保存的覆写。", func() tea.Msg {
					ctx, cancel := context.WithTimeout(m.ctx, 3*time.Minute)
					defer cancel()
					_, err := m.client.switchSubscription(ctx, id)
					return terminalActionResult{text: "已切换并应用所选订阅。", err: err}
				})
			}
			if key == "u" {
				if sub.Kind != "url" {
					m.notice = "本地文件订阅请按 f 重新导入。"
					return nil
				}
				return m.action(m.apiAction(http.MethodPost, path, struct{}{}, "订阅已更新；重新应用后生效。"))
			}
			if key == "d" || key == "delete" {
				m.confirm("删除订阅“"+name+"”？当前运行配置保持有效。", m.apiAction(http.MethodDelete, path, nil, "订阅已移除。"))
			}
		}
	case 3:
		if key == "g" {
			m.group = ""
			m.selected = 0
			return nil
		}
		if key == "enter" {
			if g := m.currentGroup(); g != nil {
				if strings.ToLower(g.Type) != "selector" && strings.ToLower(g.Type) != "select" {
					m.notice = "此代理组自动选择节点，不能手动切换。"
					return nil
				}
				if m.selected < len(g.All) {
					return m.action(m.apiAction(http.MethodPost, "/api/groups", proxySelection{Group: g.Name, Name: g.All[m.selected]}, "节点已切换。"))
				}
			} else if m.selected < len(m.data.groups) {
				m.group = m.data.groups[m.selected].Name
				m.selected = 0
				m.scroll = 0
			}
		}
		if key == "t" {
			if g := m.currentGroup(); g != nil {
				return m.action(m.measureGroup(*g))
			}
		}
	case 4:
		switch key {
		case "d":
			return m.action(func() tea.Msg {
				var result terminalDiscovery
				ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
				defer cancel()
				err := m.client.request(ctx, http.MethodGet, "/api/core/discovery", nil, &result)
				return terminalDiscoveryResult{data: result, err: err}
			})
		case "i":
			m.confirm("安装或启动当前面板管理的内核？已有内核文件与配置会复用。", m.apiAction(http.MethodPost, "/api/core/connect", struct{}{}, "安装或启动任务已提交，可在此查看进度。"))
		case "p":
			m.confirm("恢复使用面板内核？现有外部服务继续由原应用管理。", m.apiAction(http.MethodPost, "/api/core/select", map[string]string{"id": "panel"}, "已恢复面板内核接入方式，按 i 安装或启动。"))
		case "enter":
			if m.selected < len(m.discovery.Candidates) {
				c := m.discovery.Candidates[m.selected]
				m.confirm("使用检测到的内核“"+terminalSafeText(c.Source)+"”？连接信息会保存。", func() tea.Msg {
					ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
					defer cancel()
					err := m.client.request(ctx, http.MethodPost, "/api/core/select", map[string]string{"id": c.ID}, nil)
					if err == nil && !c.Connected {
						err = m.client.request(ctx, http.MethodPost, "/api/core/connect", struct{}{}, nil)
					}
					return terminalActionResult{text: "已选择内核，启动和连接状态见上方。", err: err}
				})
			}
		}
	case 5:
		if key == "l" {
			return m.action(func() tea.Msg {
				ctx, cancel := context.WithTimeout(m.ctx, 8*time.Second)
				defer cancel()
				text, err := m.service.Logs(ctx)
				return terminalTextResult{title: "面板服务日志", text: text, err: err}
			})
		}
	case 6:
		if key == "enter" {
			switch m.selected {
			case 0:
				return m.action(m.checkPanelUpdate())
			case 1:
				m.confirm("更新面板安装的 Mihomo 至官方稳定版？校验下载完成后会重启内核，代理连接可能短暂中断。", m.apiAction(http.MethodPost, "/api/core/update", struct{}{}, "内核更新已提交，请在 Mihomo 内核页面查看进度。"))
			case 2:
				m.confirm("卸载面板服务与 miproxy 命令？配置、环境文件和 Mihomo 文件保留；面板管理的内核将停止。", func() tea.Msg {
					ctx, cancel := context.WithTimeout(m.ctx, 45*time.Second)
					defer cancel()
					text, err := m.service.Uninstall(ctx)
					return terminalActionResult{text: text, err: err, quit: true}
				})
			}
		}
	}
	return nil
}

func (m terminalModel) measureGroup(group runtimeGroup) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 3*time.Minute)
		defer cancel()
		delays := map[string]string{}
		var mu sync.Mutex
		var first error
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for _, name := range group.All {
			if ctx.Err() != nil {
				break
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				defer func() { <-sem }()
				var result struct {
					Delay  int
					Status string
				}
				err := m.client.request(ctx, http.MethodPost, "/api/nodes/delay", map[string]string{"name": name}, &result)
				label := result.Status
				if result.Delay > 0 {
					label = fmt.Sprintf("%d ms", result.Delay)
				}
				if label == "timeout" {
					label = "超时"
				}
				if label == "unreachable" {
					label = "不可达"
				}
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					label = "失败"
					if first == nil {
						first = err
					}
				}
				delays[name] = label
			}(name)
		}
		wg.Wait()
		if ctx.Err() != nil {
			first = ctx.Err()
		}
		return terminalDelayResult{delays: delays, err: first}
	}
}

func (m *terminalModel) formKey(key tea.KeyMsg) tea.Cmd {
	f := m.form
	switch key.String() {
	case "esc":
		m.form = nil
		return nil
	case "backspace", "ctrl+h":
		r := []rune(f.value)
		if len(r) > 0 {
			f.value = string(r[:len(r)-1])
		}
		return nil
	case "ctrl+u":
		f.value = ""
		return nil
	case "enter":
		value := strings.TrimSpace(f.value)
		if value == "" {
			m.notice = "请输入" + f.label
			return nil
		}
		switch f.kind {
		case "url-name":
			f.kind = "url"
			f.name = value
			f.value = ""
			f.label = "订阅链接（输入隐藏）"
			f.secret = true
			return nil
		case "file-name":
			f.kind = "file"
			f.name = value
			f.value = ""
			f.label = "YAML 文件的绝对路径"
			return nil
		case "url":
			u, err := url.Parse(value)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				m.notice = "请输入完整的 HTTP 或 HTTPS 订阅地址。"
				return nil
			}
			name := f.name
			m.form = nil
			return m.action(m.apiAction(http.MethodPost, "/api/subscriptions", map[string]string{"name": name, "url": value}, "订阅已导入，选中后按 Enter 应用。"))
		case "file":
			name := f.name
			m.form = nil
			return m.action(func() tea.Msg {
				if !filepath.IsAbs(value) {
					return terminalActionResult{err: errors.New("请输入绝对路径")}
				}
				snapshot, err := readConfigFile(value)
				if err == nil && !snapshot.Exists {
					err = errors.New("文件不存在")
				}
				if err == nil {
					ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
					defer cancel()
					err = m.client.request(ctx, http.MethodPost, "/api/subscriptions", map[string]string{"name": name, "content": snapshot.Content}, nil)
				}
				return terminalActionResult{text: "本地订阅已导入，选中后按 Enter 应用。", err: err}
			})

		}
	default:
		if key.Type == tea.KeyRunes && len([]rune(f.value)) < 4096 {
			for _, r := range key.Runes {
				if !unicode.IsControl(r) {
					f.value += string(r)
				}
			}
		}
	}
	return nil
}
