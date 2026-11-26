package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/runtimeenv"
)

// ConfigureAccess changes Web authentication through the private local socket.
// Passwords are read without echo and are never accepted in process arguments.
func ConfigureAccess() error {
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return errors.New("访问设置需要交互终端，请在设备本机或 SSH 中运行 miproxy-panel access")
	}
	path := runtimeenv.Value("MIPROXY_ENV_FILE", "/etc/miproxy-panel.env")
	if err := loadTerminalEnvironment(path, os.Getenv("MIPROXY_ENV_FILE") != ""); err != nil {
		return err
	}
	configPath, err := terminalConfigPath()
	if err != nil {
		return err
	}
	parent, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	client := newTerminalClient(runtimeenv.SocketPath(configPath))
	defer client.http.CloseIdleConnections()
	var current struct {
		PasswordSet bool `json:"passwordSet"`
	}
	ctx, finish := context.WithTimeout(parent, 5*time.Second)
	err = client.request(ctx, "GET", "/api/settings/access", nil, &current)
	finish()
	if err != nil {
		return err
	}
	fmt.Println("Web 访问设置")
	if current.PasswordSet {
		fmt.Println("当前已设置密码，登录用户名为 admin。")
	} else {
		fmt.Println("当前仅允许本机访问。设置密码后可通过已配置的监听地址远程登录。")
	}
	fmt.Print("新密码（输入隐藏，留空恢复仅本机访问）：")
	password, err := term.ReadPassword(os.Stdin.Fd())
	fmt.Println()
	if err != nil {
		return err
	}
	defer clear(password)
	fmt.Print("再次输入密码：")
	confirmation, err := term.ReadPassword(os.Stdin.Fd())
	fmt.Println()
	if err != nil {
		return err
	}
	defer clear(confirmation)
	if string(password) != string(confirmation) {
		return errors.New("两次输入的密码不一致，未修改设置")
	}
	ctx, finish = context.WithTimeout(parent, 15*time.Second)
	defer finish()
	if err = client.request(ctx, "POST", "/api/settings/access", map[string]string{"password": string(password)}, nil); err != nil {
		return err
	}
	if len(password) == 0 {
		fmt.Println("已恢复仅本机访问。浏览器现有登录凭据可以清除。")
	} else {
		fmt.Println("访问密码已保存，浏览器使用 admin 和新密码登录；不需要重启服务。")
	}
	return nil
}
