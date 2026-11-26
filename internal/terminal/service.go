package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type terminalServiceStatus struct {
	Installed bool
	Running   bool
	Enabled   bool
	Manager   string
	Detail    string
}

type terminalService struct {
	manager     string
	binary      string
	servicePath string
	detail      string
	run         func(context.Context, string, ...string) (string, error)
	stat        func(string) (os.FileInfo, error)
	readFile    func(string) ([]byte, error)
	readlink    func(string) (string, error)
	remove      func(string) error
	euid        func() int
}

const terminalInstallMarker = "/etc/miproxy-panel.install"

func newTerminalService() terminalService {
	s := terminalService{
		run: terminalRunCommand, stat: os.Stat, readFile: os.ReadFile,
		readlink: os.Readlink, remove: os.Remove, euid: os.Geteuid,
	}
	if runtime.GOOS != "linux" {
		s.detail = "当前系统未配置原生服务；可在另一终端运行 miproxy-panel。"
		return s
	}
	if _, err := os.Stat("/etc/openwrt_release"); err == nil {
		s.manager, s.binary, s.servicePath = "procd", "/usr/bin/miproxy-panel", "/etc/init.d/miproxy-panel"
		return s
	}
	if _, err := exec.LookPath("systemctl"); err == nil {
		s.manager, s.binary, s.servicePath = "systemd", "/usr/local/bin/miproxy-panel", "/etc/systemd/system/miproxy-panel.service"
		return s
	}
	s.detail = "没有发现 systemd 或 OpenWrt procd；请使用部署环境自身的服务管理方式。"
	return s
}

func (s terminalService) Status(ctx context.Context) terminalServiceStatus {
	status := terminalServiceStatus{Manager: s.manager, Detail: s.detail}
	if s.manager == "" {
		return status
	}
	if _, err := s.stat(s.servicePath); err != nil {
		status.Detail = "尚未安装系统服务，请先执行 deploy/install.sh。"
		return status
	}
	status.Installed = true
	if s.manager == "systemd" {
		output, err := s.run(ctx, "systemctl", "is-active", "miproxy-panel.service")
		status.Running = err == nil && strings.TrimSpace(output) == "active"
		if err != nil && !strings.Contains(output, "inactive") && !strings.Contains(output, "failed") {
			status.Detail = terminalServiceError(output, err).Error()
		}
		output, err = s.run(ctx, "systemctl", "is-enabled", "miproxy-panel.service")
		status.Enabled = err == nil && strings.TrimSpace(output) == "enabled"
	} else {
		_, err := s.run(ctx, s.servicePath, "running")
		status.Running = err == nil
		_, err = s.run(ctx, s.servicePath, "enabled")
		status.Enabled = err == nil
	}
	return status
}

func (s terminalService) Action(ctx context.Context, action string) (string, error) {
	labels := map[string]string{"start": "启动", "stop": "停止", "restart": "重启", "enable": "开启开机启动", "disable": "关闭开机启动"}
	label, ok := labels[action]
	if !ok {
		return "", errors.New("不支持的服务操作")
	}
	if s.manager == "" {
		return "", errors.New(s.detail)
	}
	if _, err := s.stat(s.servicePath); err != nil {
		return "", errors.New("尚未安装系统服务，请先执行 deploy/install.sh")
	}
	if s.euid() != 0 {
		return "", errors.New("服务管理需要 root 权限，请以 root 身份运行 miproxy")
	}
	var output string
	var err error
	if s.manager == "systemd" {
		output, err = s.run(ctx, "systemctl", action, "miproxy-panel.service")
	} else {
		output, err = s.run(ctx, s.servicePath, action)
	}
	if err != nil {
		return "", fmt.Errorf("%s失败：%w", label, terminalServiceError(output, err))
	}
	return label + "完成。", nil
}

func (s terminalService) Logs(ctx context.Context) (string, error) {
	if s.manager == "" {
		return "", errors.New(s.detail)
	}
	var output string
	var err error
	if s.manager == "systemd" {
		output, err = s.run(ctx, "journalctl", "-u", "miproxy-panel.service", "--no-pager", "-n", "80", "-o", "short-iso")
	} else {
		output, err = s.run(ctx, "logread", "-e", "miproxy-panel")
		lines := strings.Split(strings.TrimSpace(output), "\n")
		if len(lines) > 80 {
			output = strings.Join(lines[len(lines)-80:], "\n")
		}
	}
	if err != nil {
		return "", terminalServiceError(output, err)
	}
	if strings.TrimSpace(output) == "" {
		return "暂无面板服务日志。", nil
	}
	return output, nil
}

// Only installations recorded by our installer may remove host files. Configuration,
// downloaded cores and the environment file are deliberately outside this list.
func (s terminalService) Uninstall(ctx context.Context) (string, error) {
	if s.manager == "" {
		return "", errors.New(s.detail)
	}
	if s.euid() != 0 {
		return "", errors.New("卸载需要 root 权限，请以 root 身份运行 miproxy")
	}
	marker, err := s.readFile(terminalInstallMarker)
	expected := "miproxy-panel-install-v1\nmanager=" + s.manager + "\nbinary=" + s.binary + "\n"
	if err != nil || string(marker) != expected {
		return "", errors.New("未找到匹配的安装记录；为保留已有部署，请使用原安装方式卸载")
	}
	linkPath := strings.TrimSuffix(s.binary, "-panel")
	link, err := s.readlink(linkPath)
	if err != nil || link != s.binary {
		return "", errors.New("miproxy 命令已被修改，已取消卸载；请先检查安装路径")
	}
	unit, err := s.readFile(s.servicePath)
	if err != nil || !strings.Contains(string(unit), s.binary) {
		return "", errors.New("服务定义已被修改，已取消卸载；请使用当前部署方式卸载")
	}
	if _, err = s.Action(ctx, "disable"); err != nil {
		return "", err
	}
	if _, err = s.Action(ctx, "stop"); err != nil {
		return "", err
	}
	for _, path := range []string{s.servicePath, linkPath, s.binary, terminalInstallMarker} {
		if err = s.remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("服务已停止，但移除 %s 失败：%w", path, err)
		}
	}
	if s.manager == "systemd" {
		if output, err := s.run(ctx, "systemctl", "daemon-reload"); err != nil {
			return "", fmt.Errorf("面板文件已移除，但刷新 systemd 失败：%w", terminalServiceError(output, err))
		}
	}
	return "面板服务与命令已移除，配置、环境文件和 Mihomo 内核均已保留。", nil
}

func terminalServiceError(output string, err error) error {
	if message := strings.TrimSpace(output); message != "" {
		return fmt.Errorf("%s (%w)", message, err)
	}
	return err
}

// Logs and service failures are bounded even when a host command emits too much.
type terminalCommandOutput struct{ data []byte }

func (b *terminalCommandOutput) Write(data []byte) (int, error) {
	const limit = 64 << 10
	n := len(data)
	if n >= limit {
		b.data = append(b.data[:0], data[n-limit:]...)
	} else {
		if excess := len(b.data) + n - limit; excess > 0 {
			copy(b.data, b.data[excess:])
			b.data = b.data[:len(b.data)-excess]
		}
		b.data = append(b.data, data...)
	}
	return n, nil
}

func terminalRunCommand(ctx context.Context, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	output := &terminalCommandOutput{}
	command.Stdout, command.Stderr = output, output
	err := command.Run()
	return string(output.data), err
}
