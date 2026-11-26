package terminal

import (
	"context"
	"errors"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m terminalModel) checkPanelUpdate() tea.Cmd {
	return func() tea.Msg {
		name, err := currentPanelAsset()
		if err != nil {
			return terminalReleaseResult{err: err}
		}
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		client := newPanelReleaseClient()
		defer client.CloseIdleConnections()
		release, err := checkPanelRelease(ctx, client, name)
		return terminalReleaseResult{release: release, err: err}
	}
}
func terminalWaitPanelUpdate(events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return terminalActionResult{err: errors.New("面板更新任务意外结束")}
		}
		return event
	}
}
func (m terminalModel) downloadPanelUpdate(release panelRelease) tea.Cmd {
	return func() tea.Msg {
		events := make(chan tea.Msg, 8)
		go func() {
			defer close(events)
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Minute)
			defer cancel()
			finish := func(text string, err error) {
				select {
				case events <- terminalActionResult{text: text, err: err}:
				case <-m.ctx.Done():
				}
			}
			if m.service.manager == "" {
				finish("", errors.New(m.service.detail))
				return
			}
			if m.service.euid() != 0 {
				finish("", errors.New("更新需要 root 权限，请以 root 身份运行 miproxy"))
				return
			}
			marker, err := m.service.readFile(terminalInstallMarker)
			expected := "miproxy-panel-install-v1\nmanager=" + m.service.manager + "\nbinary=" + m.service.binary + "\n"
			if err != nil || string(marker) != expected {
				finish("", errors.New("未找到匹配的安装记录，请使用原部署方式更新"))
				return
			}
			directory, err := os.MkdirTemp("", "miproxy-release-*")
			if err != nil {
				finish("", err)
				return
			}
			defer os.RemoveAll(directory)
			client := newPanelReleaseClient()
			defer client.CloseIdleConnections()
			binary, err := downloadPanelRelease(ctx, client, release, directory, func(progress panelDownloadProgress) {
				select {
				case events <- progress:
				default:
				}
			})
			if err != nil {
				finish("", err)
				return
			}
			text, err := m.service.updateBinary(ctx, binary, release.Tag)
			if err == nil {
				text = "已从 GitHub 更新至 " + release.Tag + "。" + text + " 退出并重新运行 miproxy 使用新版终端。"
			}
			finish(text, err)
		}()
		return terminalPanelUpdate{events: events}
	}
}
