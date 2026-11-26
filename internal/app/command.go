// Package app dispatches the executable's Web and terminal entry modes.
package app

import (
	"errors"
	"fmt"
	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/buildinfo"
	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/server"
	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/terminal"
	"path/filepath"
)

func Run(name string, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "--version", "version":
			fmt.Println("MiProxy Panel", buildinfo.Version)
			return nil
		case "--help", "-h", "help":
			fmt.Println(`MiProxy Panel

  miproxy                     打开终端管理面板
  miproxy-panel tui            打开终端管理面板
  miproxy-panel [serve]        启动后台 Web 服务
  miproxy-panel access         配置 Web 访问密码
  miproxy --version            查看版本

终端读取 /etc/miproxy-panel.env，或 MIPROXY_ENV_FILE 指定文件。认证在应用内设置，无需密码环境变量。`)
			return nil
		case "tui":
			if len(args) != 1 {
				return errors.New("tui 不接受额外参数")
			}
			return terminal.Run()
		case "serve":
			if len(args) != 1 {
				return errors.New("serve 不接受额外参数")
			}
			return server.Run()
		case "access":
			if len(args) != 1 {
				return errors.New("access 不接受额外参数")
			}
			return terminal.ConfigureAccess()
		default:
			return fmt.Errorf("未知命令 %q，运行 --help 查看用法", args[0])
		}
	}
	if filepath.Base(name) == "miproxy" {
		return terminal.Run()
	}
	return server.Run()
}
