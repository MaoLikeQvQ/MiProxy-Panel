package terminal

import (
	"fmt"
	"net"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/buildinfo"
	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/safeoutput"
)

const terminalBackground = "\x1b[48;2;23;34;52m\x1b[38;5;253m"

func (m terminalModel) paint(s, kind string) string {
	if m.noColor {
		return s
	}
	code := map[string]string{"cyan": "\x1b[38;5;116m", "dim": "\x1b[38;5;247m", "line": "\x1b[38;5;240m", "green": "\x1b[38;5;151m", "amber": "\x1b[38;5;222m", "red": "\x1b[38;5;210m", "strong": "\x1b[1;38;5;255m", "active": "\x1b[48;5;116m\x1b[38;5;234m\x1b[1m"}[kind]
	return code + s + "\x1b[0m" + terminalBackground
}
func terminalFit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = ansi.Truncate(s, width, "…")
	return s + strings.Repeat(" ", max(0, width-ansi.StringWidth(s)))
}

func terminalInputTail(value string, width int) string {
	if ansi.StringWidth(value) <= width {
		return value
	}
	return ansi.TruncateLeft(value, ansi.StringWidth(value)-width+1, "…")
}

// Keep action labels in place while only the list scrolls. The selected row
// stays visible even in a short SSH window with many subscriptions or nodes.
func terminalListViewport(lines []string, header, count, selected, height int) []string {
	if len(lines) <= height || count == 0 {
		return lines
	}
	header = min(header, max(0, height-1))
	listHeight := min(count, height-header)
	start := min(max(0, selected-listHeight+1), max(0, count-listHeight))
	result := append([]string{}, lines[:header]...)
	result = append(result, lines[header+start:header+start+listHeight]...)
	if remaining := height - len(result); remaining > 0 {
		tail := lines[header+count:]
		result = append(result, tail[:min(remaining, len(tail))]...)
	}
	return result
}
func terminalWrap(text string, width int) []string {
	if width <= 0 {
		return nil
	}
	var result []string
	var b strings.Builder
	n := 0
	for _, r := range text {
		w := ansi.StringWidth(string(r))
		if r == '\n' || n+w > width {
			result = append(result, b.String())
			b.Reset()
			n = 0
			if r == '\n' {
				continue
			}
		}
		b.WriteRune(r)
		n += w
	}
	return append(result, b.String())
}
func terminalBytes(n float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", n, units[i])
	}
	return fmt.Sprintf("%.2f %s", n, units[i])
}
func terminalYes(value bool, yes, no string) string {
	if value {
		return yes
	}
	return no
}
func (m terminalModel) selection(index int, text string) string {
	prefix := "  "
	if index == m.selected {
		prefix = "› "
	}
	text = prefix + safeoutput.Text(text)
	if index == m.selected {
		if !m.nav {
			return m.paint(text, "cyan")
		}
		return text
	}
	return m.paint(text, "dim")
}
func (m terminalModel) hint() string {
	if m.nav {
		return "↑↓ 选择菜单  Enter 进入  1–7 快捷进入  q 退出"
	}
	switch m.page {
	case 0:
		return "r 刷新  Tab 菜单  q 退出"
	case 1:
		return "↑↓ 选择  Enter 操作  Esc 菜单"
	case 2:
		return "↑↓ 选中  Enter 切换应用  a 导入  d 删除  Esc 菜单"
	case 3:
		if m.group != "" {
			return "↑↓ 选择  Enter 切换  t 本组测速  g 返回代理组  Esc 返回"
		}
		return "↑↓ 选择代理组  Enter 查看节点  Esc 菜单"
	case 4:
		return "d 检测  ↑↓ 选择  Enter 接入  i 安装/启动  p 面板内核  Esc 菜单"
	case 5:
		return "l 面板日志  ↑↓ / PgUp / PgDn 滚动  r 检查  Esc 菜单"
	case 6:
		return "↑↓ 选择  Enter 操作  Esc 菜单"
	}
	return "Esc 返回  q 退出"
}
func (m terminalModel) View() string {
	width, height := m.width, m.height
	if width < 40 || height < 16 {
		return "MiProxy\n终端窗口过小，请扩大至至少 40 列 × 16 行。\n按 q 或 Ctrl+C 退出。"
	}
	compact := width < 94
	inner := width - 4
	contentWidth := inner
	if !compact {
		contentWidth -= 26
	}
	status := terminalYes(m.data.online, "● 面板运行中", "○ 面板未连接") + "  " + terminalYes(m.data.core.Connected, "● 内核已连接", "○ 内核未连接")
	title := m.paint("MiProxy", "cyan") + m.paint(" / "+terminalMenus[m.page], "strong")
	rows := []string{title, m.paint("$ miproxy", "dim") + "  " + m.paint(status, terminalYes(m.data.online, "green", "amber")), m.paint(strings.Repeat("─", inner), "line")}
	bodyHeight := height - 8
	if compact {
		rows = append(rows, m.paint(fmt.Sprintf("%d / %d  %s", m.page+1, len(terminalMenus), terminalMenus[m.page]), "cyan")+m.paint("   Tab 切换菜单 / 操作", "dim"))
		bodyHeight--
	}
	lines := m.pageLines(contentWidth)
	if m.form != nil {
		f := m.form
		value := f.value
		if f.secret {
			value = strings.Repeat("•", len([]rune(value)))
		}
		lines = []string{m.paint(f.title, "strong"), "", m.paint(f.label, "dim"), m.paint(terminalInputTail(value, contentWidth-1)+"▏", "cyan"), "", m.paint("Enter 继续  Esc 取消  Ctrl+U 清空", "dim")}
	} else if m.confirmation != nil {
		lines = []string{m.paint("确认操作", "amber"), ""}
		for _, line := range terminalWrap(m.confirmation.text, contentWidth) {
			lines = append(lines, line)
		}
		lines = append(lines, "", m.paint("y 确认执行     n / Esc 取消", "cyan"))
	} else if m.textTitle != "" {
		lines = []string{m.paint(m.textTitle, "strong"), ""}
		lines = append(lines, m.textLines[min(m.textOffset, len(m.textLines)):]...)
	}
	// Keep large lists usable on a 24-row SSH terminal; controls stay fixed below.
	offset := 0
	if m.form == nil && m.confirmation == nil && m.textTitle == "" {
		offset = min(m.scroll, max(0, len(lines)-bodyHeight))
		if m.data.pageErr == "" && (m.page == 2 || m.page == 3) {
			header := 3
			if m.page == 2 {
				header = 5
			}
			lines = terminalListViewport(lines, header, m.itemCount(), m.selected, bodyHeight)
			offset = 0
		}
	}
	lines = lines[offset:]
	for i := 0; i < bodyHeight; i++ {
		right := ""
		if i < len(lines) {
			right = lines[i]
		}
		if compact {
			rows = append(rows, terminalFit(right, inner))
			continue
		}
		left := ""
		if i == 0 {
			left = m.paint(" 管理菜单", "dim")
		}
		if i >= 2 && i < 2+len(terminalMenus) {
			ix := i - 2
			text := fmt.Sprintf(" %d  %s", ix+1, terminalMenus[ix])
			if ix == m.page {
				if m.nav {
					left = m.paint(terminalFit("›"+text, 24), "active")
				} else {
					left = m.paint("›"+text, "cyan")
				}
			} else {
				left = m.paint(" "+text, "dim")
			}
		}
		if i == 11 {
			left = m.paint("  0  退出菜单", "dim")
		}
		rows = append(rows, terminalFit(left, 24)+m.paint("│ ", "line")+terminalFit(right, inner-26))
	}
	rows = append(rows, m.paint(strings.Repeat("─", inner), "line"))
	notice := m.notice
	kind := "green"
	if m.failed {
		kind = "red"
	} else if m.busy {
		kind = "amber"
	}
	if notice == "" {
		notice = m.data.err
		kind = "amber"
	}
	if notice == "" {
		notice = "退出菜单后，后台服务继续运行。"
		kind = "dim"
	}
	rows = append(rows, m.paint(safeoutput.Text(notice), kind))
	hint := m.hint()
	if m.form != nil {
		hint = "Enter 继续  Esc 取消"
	}
	if m.confirmation != nil {
		hint = "y 确认  n / Esc 取消"
	}
	if m.textTitle != "" {
		hint = "↑↓ / PgUp / PgDn 滚动  Esc 返回"
	}
	rows = append(rows, m.paint(hint, "dim"))
	for len(rows) < height {
		rows = append(rows, "")
	}
	if len(rows) > height {
		rows = rows[:height]
	}
	for i, line := range rows {
		line = "  " + terminalFit(line, inner) + "  "
		if !m.noColor {
			line = terminalBackground + line + "\x1b[0m"
		}
		rows[i] = line
	}
	return strings.Join(rows, "\n")
}

func (m terminalModel) pageLines(width int) []string {
	title := m.paint(terminalMenus[m.page], "strong")
	if m.data.pageErr != "" {
		return append([]string{title, ""}, terminalWrap(safeoutput.Text(m.data.pageErr), max(20, width-30))...)
	}
	switch m.page {
	case 0:
		owner := "面板管理"
		if m.data.core.External {
			owner = "外部应用管理"
		}
		host, port, err := net.SplitHostPort(m.address)
		address := m.address
		if err == nil {
			if host == "" || host == "0.0.0.0" || host == "::" {
				address = "设备地址:" + port
			} else {
				address = net.JoinHostPort(host, port)
			}
		}
		lines := []string{title, m.paint(runtime.GOOS+" / "+runtime.GOARCH, "dim"), "", m.paint("服务状态", "dim"), m.paint("  "+terminalYes(m.data.online, "● 面板服务    运行中", "○ 面板服务    未连接"), terminalYes(m.data.online, "green", "amber")), m.paint("  "+terminalYes(m.data.core.Connected, "● Mihomo      已连接", "○ Mihomo      未连接"), terminalYes(m.data.core.Connected, "green", "amber")), "  接入来源    " + owner, "", m.paint("Web 面板", "dim"), "  http://" + address, "", m.paint("实时流量", "dim")}
		if rate := m.data.overview.Traffic.Rate; rate != nil {
			lines = append(lines, m.paint("  ↓ "+terminalBytes(rate.Download)+"/s   ↑ "+terminalBytes(rate.Upload)+"/s", "strong"))
		} else {
			lines = append(lines, m.paint("  暂无流量采样", "dim"))
		}
		if c := m.data.overview.Counters; c != nil {
			lines = append(lines, fmt.Sprintf("  活跃连接 %d    累计 %s", c.Active, terminalBytes(float64(c.Uploaded+c.Downloaded))))
		}
		if m.data.core.Version != "" {
			lines = append(lines, "", m.paint("内核版本", "dim"), "  "+safeoutput.Text(m.data.core.Version))
		}
		if m.data.service.Installed {
			lines = append(lines, "", m.paint("开机启动："+terminalYes(m.data.service.Enabled, "已开启", "已关闭"), "dim"))
		}
		return lines
	case 1:
		lines := []string{title, m.paint("服务管理器："+terminalYes(m.data.service.Manager != "", m.data.service.Manager, "未配置"), "dim"), ""}
		for i, option := range m.serviceOptions() {
			lines = append(lines, m.selection(i, option.label))
		}
		lines = append(lines, "", m.paint("停止或重启面板会影响它启动的内核。", "amber"))
		for _, line := range terminalWrap(safeoutput.Text(m.data.service.Detail), width) {
			lines = append(lines, m.paint(line, "dim"))
		}
		return lines
	case 2:
		lines := []string{title, m.paint("a 导入订阅    f 本地 YAML    u 更新", "cyan"), m.paint("d 删除订阅    Enter 切换应用", "cyan"), "", m.paint(fmt.Sprintf("共 %d 个订阅 · › 表示选中，[使用中] 表示已应用", len(m.data.subscriptions)), "dim")}
		if len(m.data.subscriptions) == 0 {
			return append(lines, "", "还没有订阅。", m.paint("按 a 导入订阅链接，或 f 导入本地 YAML。", "dim"))
		}
		for i, sub := range m.data.subscriptions {
			state := ""
			if sub.Applied {
				state = "  [使用中]"
			}
			lines = append(lines, m.selection(i, fmt.Sprintf("%s%s  ·  %s", sub.Name, state, sub.Updated.Local().Format("01-02 15:04"))))
		}
		if sub := m.selectedSubscription(); sub != nil {
			lines = append(lines, "", m.paint(safeoutput.Text(sub.Name), "strong"), "  类型："+terminalYes(sub.Kind == "url", "远程订阅", "本地 YAML"))
			if sub.Usage != nil {
				u := sub.Usage
				lines = append(lines, "  已用流量："+terminalBytes(float64(u.Download+u.Upload)))
				if u.Total > 0 {
					lines = append(lines, "  总流量：  "+terminalBytes(float64(u.Total)))
				}
				if u.Expire > 0 {
					lines = append(lines, "  到期：    "+time.Unix(u.Expire, 0).Local().Format("2006-01-02"))
				}
			}
		}
		return append(lines, "", m.paint("应用前校验配置，并叠加已保存的覆写。", "dim"))
	case 3:
		lines := []string{title, m.paint("选择代理组后查看节点；自动组只读。", "dim"), ""}
		if g := m.currentGroup(); g != nil {
			lines[1] = m.paint(safeoutput.Text(g.Name)+" / "+safeoutput.Text(g.Type), "dim")
			for i, node := range g.All {
				delay := m.delays[node]
				if delay == "" {
					delay = "未测速"
				}
				current := ""
				if node == g.Now {
					current = "  当前选择"
				}
				lines = append(lines, m.selection(i, node+"  "+delay+current))
			}
		} else {
			for i, g := range m.data.groups {
				lines = append(lines, m.selection(i, g.Name+"  → "+g.Now))
			}
			if len(m.data.groups) == 0 {
				lines = append(lines, "暂无代理组，请先连接内核并应用订阅。")
			}
		}
		return lines
	case 4:
		c := m.data.core
		lines := []string{title, m.paint("当前来源："+safeoutput.Text(c.Source), "dim"), "", m.paint("安装与连接状态", "dim"), "  " + terminalYes(c.Connected, "● 已连接", "○ 未连接") + "  " + safeoutput.Text(c.Version), "  " + safeoutput.Text(c.Detail)}
		if c.Total > 0 {
			ratio := min(1., float64(c.Downloaded)/float64(c.Total))
			n := int(ratio * 28)
			lines = append(lines, m.paint("  "+strings.Repeat("━", n)+strings.Repeat("─", 28-n), "cyan"), fmt.Sprintf("  %.0f%%  %s / %s  %s/s", ratio*100, terminalBytes(float64(c.Downloaded)), terminalBytes(float64(c.Total)), terminalBytes(c.Speed)))
		} else if c.Busy {
			lines = append(lines, "  "+terminalBytes(float64(c.Downloaded))+"  "+terminalBytes(c.Speed)+"/s")
		}
		if c.DisabledReason != "" {
			for _, line := range terminalWrap(safeoutput.Text(c.DisabledReason), width) {
				lines = append(lines, m.paint(line, "amber"))
			}
		}
		lines = append(lines, "", m.paint("本机检测结果 · 按 d 重新检测", "dim"))
		for i, c := range m.discovery.Candidates {
			lines = append(lines, m.selection(i, c.Source+"  "+terminalYes(c.Connected, "可连接", terminalYes(c.Running, "运行中 / 接口不可用", "未启动"))))
			if i == m.selected {
				lines = append(lines, m.paint("  "+safeoutput.Text(c.Binary), "dim"))
				if c.Reason != "" {
					lines = append(lines, m.paint("  "+safeoutput.Text(c.Reason), "amber"))
				}
			}
		}
		for _, warning := range m.discovery.Warnings {
			lines = append(lines, m.paint(safeoutput.Text(warning), "amber"))
		}
		if len(m.discovery.Candidates) == 0 {
			lines = append(lines, "按 d 检测，或 i 安装并启动面板内核。")
		}
		return lines
	case 5:
		lines := []string{title, "", m.paint(terminalYes(m.data.online, "✓ 本机面板接口正常", "! 本机面板接口未连接"), terminalYes(m.data.online, "green", "amber")), m.paint(terminalYes(m.data.core.Connected, "✓ Mihomo 控制接口正常", "! Mihomo 控制接口未连接"), terminalYes(m.data.core.Connected, "green", "amber")), "", m.paint("内核实时日志 · 本次终端会话最近 200 条", "dim")}
		if !m.data.core.Connected {
			return append(lines, "内核连接后自动读取日志。", m.paint("按 l 查看面板服务日志。", "dim"))
		}
		if len(m.logLines) == 0 {
			lines = append(lines, m.paint("等待内核日志…", "dim"))
		}
		for i := len(m.logLines) - 1; i >= 0; i-- {
			lines = append(lines, m.paint(m.logLines[i], "dim"))
		}
		return lines
	case 6:
		lines := []string{title, m.paint("面板更新源：github.com/"+panelRepository, "dim"), "", m.selection(0, "检查并更新面板（GitHub）"), m.selection(1, "更新面板安装的 Mihomo"), m.selection(2, "卸载面板与命令"), "", m.paint("更新与卸载会先显示影响范围，再确认执行。", "dim"), m.paint("卸载保留配置、环境文件及 Mihomo 文件。", "dim")}
		lines = append(lines, "", m.paint("当前面板版本："+buildinfo.Version, "dim"))
		if m.panelRelease != nil {
			lines = append(lines, m.paint("GitHub 最新版本："+m.panelRelease.Tag, "cyan"))
		}
		if m.data.core.Busy {
			lines = append(lines, "", m.paint("内核任务进行中："+safeoutput.Text(m.data.core.Detail), "cyan"))
		}
		return lines
	}
	return []string{title}
}
