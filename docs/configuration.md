# 配置与已有内核接入

系统服务和终端读取 /etc/miproxy-panel.env。文件权限应为 0600，终端与服务使用相同的配置路径；修改后重启服务。

| 变量 | 默认值或用途 |
| --- | --- |
| `PANEL_SOCKET` | 终端本机控制接口的绝对路径，默认配置目录下的 `.miproxy/panel.sock`；不会开放 TCP 端口。 |
| `PANEL_LISTEN` | `127.0.0.1:8787` |
| `PANEL_PASSWORD` | HTTP Basic Auth 密码。绑定非回环地址时必填。远程访问建议经 HTTPS 反向代理。 |
| `PANEL_CONFIG` | 面板访问的配置文件路径，默认 `./config/config.yaml`，父目录须存在。 |
| `PANEL_EXTERNAL_CONFIG` | 设为 `true` 时，由外部应用保存并重载配置。面板保留草稿校验和运行控制。 |
| `MIHOMO_HOME` | Mihomo 校验用的工作目录（`-d`），默认取配置文件的父目录。 |
| `MIHOMO_BINARY` | 本机 Mihomo 可执行文件，默认从 `PATH` 查找 `mihomo`。面板安装的内核存在时优先使用它进行校验和启动。建议与运行实例同版本。 |
| `MIHOMO_CONTROLLER` | 默认 `http://127.0.0.1:9090`。也支持 `unix:/绝对路径.sock`。 |
| `MIHOMO_SECRET` | Mihomo 控制接口的密钥。 |
| `MIHOMO_CONFIG_PATH` | 从 Mihomo 进程看到的同一配置文件的绝对路径。仅在两个进程的挂载路径不同时设置。 |
| `MIHOMO_RELOAD_PAYLOAD` | 启用后，面板保存自己的配置文件，并通过 Mihomo 控制接口发送完整 YAML。适合内核无法读取面板配置路径的部署。默认 `false`。 |

原生部署时，可在仪表台安装并启动内核；使用已有服务时，让 Mihomo 以这份 YAML 启动，并在该配置设好 `external-controller`。目标文件应是普通文件，且在 Mihomo 允许的配置路径内。若 Mihomo 由 ShellCrash 管理且会在启动时生成配置，继续在 ShellCrash 维护其启动配置和覆写；面板覆写用于面板自己的配置草稿。透明代理和局域网流量接管由现有 Mihomo 部署配置。

### 连接本机 Mihomo 内核

将 `MIHOMO_CONTROLLER` 指向 Mihomo 内核的控制接口。若内核通过 Unix Socket 提供 API，使用 `unix:/绝对路径.sock`。例如，本机由 Sparkle 启动的 Mihomo 可以通过 `unix:/tmp/sparkle-mihomo-api.sock` 连接。面板通过这个接口读取实时指标、规则和日志，并切换代理组节点。

如果运行配置由其他程序生成，可将 `PANEL_EXTERNAL_CONFIG` 设为 `true`，并让 `PANEL_CONFIG` 指向面板自己的草稿文件。面板仍可校验和导出草稿，运行配置从生成它的程序保存并应用。

要由面板直接应用配置，可以先将当前完整配置复制到 `PANEL_CONFIG`，把 `PANEL_EXTERNAL_CONFIG` 设为 `false`，把 `MIHOMO_RELOAD_PAYLOAD` 设为 `true`，并让 `MIHOMO_HOME` 与内核使用的工作目录一致。之后点击「保存并应用」，面板校验并保存 YAML，再通过控制接口重载。重启内核时，启动程序仍按自己的启动参数加载配置。
