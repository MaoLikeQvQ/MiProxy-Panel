# 第一版验收记录

记录日期：2026-11-26。以下记录本地工作区和发布前的验证范围；云端运行与正式产物以 [Actions](https://github.com/MaoLikeQvQ/MiProxy-Panel/actions) 和 [Releases](https://github.com/MaoLikeQvQ/MiProxy-Panel/releases) 的实际记录为准。

## v0.1.0 发布记录

- 发布代码提交：`a003cb412a9842a5d795e7c333e7dc9ef741e548`。
- [打包预检 45770077798](https://github.com/MaoLikeQvQ/MiProxy-Panel/actions/runs/45770077798)：通过，生成产物并完成 Linux 安装验收，不公开 Release。
- [正式发布 45770667112](https://github.com/MaoLikeQvQ/MiProxy-Panel/actions/runs/45770667112)：通过，标签 `v0.1.0`，完成前后端检查、四套 Chromium 回归、13 目标编译、原生 amd64 Ubuntu systemd 安装验收和发布。
- [正式 Release](https://github.com/MaoLikeQvQ/MiProxy-Panel/releases/tag/v0.1.0)：已公开，19 个发布文件。从正式 Release 实际下载全部文件后，校验清单中的 18 项 SHA-256 全部一致；核对了 13 个二进制的系统、位数与字节序，以及部署包中的 4 个允许路径。
- 下载的 Apple Silicon macOS 二进制实际执行 `--version` 返回 `MiProxy Panel v0.1.0`。发布文件仅下载与校验，没有安装到 macOS 系统目录或启动新内核。

## 已通过

| 项目 | 验证方式与范围 |
| --- | --- |
| 前端类型与构建 | Node.js 24 下运行 npm --prefix web run build，包含 vue-tsc 类型检查 |
| 后端测试 | Go 1.26 下 go test -race ./... 与 go vet ./... |
| YAML 行为 | node --test web/tests/unit/yaml.test.mjs，6 项通过，覆盖订阅合并、规则、覆写与地理数据设置 |
| Web 浏览器回归 | core-dialog、discovery-ui、runtime-ui、unified-rules 四套脚本；覆盖内核对话框、已有内核选择、移动端暗色布局、日志连接条件、下载进度、订阅应用及失败提示、统一规则页添加/编辑/删除撤销、规则集地址与策略写入、外部草稿导出；API 使用测试数据 |
| 终端 | 真实伪终端中输入与菜单操作，窗口缩放、订阅导入/删除/应用、节点切换与日志；不等同于 macOS Terminal 的全部绘制行为 |
| 发布脚本 | 本地构建 13 个目标，校验二进制及配套文件的 SHA-256、版本号、部署包路径；本次新增可离线阅读的 downloads.md / usage.md；修复 macOS tar 扩展元数据导致 Linux 拒绝部署包的问题 |
| Shell 静态检查 | install.sh、deploy/install.sh、scripts/release/build.sh 的 ShellCheck 与 sh -n |
| GitHub 工作流静态检查 | actionlint；正式发布前必须经过测试和安装验收，手动运行只保留 Artifact |
| Linux ARM64 原生安装 | Docker Desktop Linux VM 内的可丢弃 Debian 13 ARM64 容器，PID 1 为真实 systemd；安装目录和服务均为真实路径，未修改 macOS 宿主系统 |
| Linux amd64 运行 | 仿真容器中启动实际 amd64 发布二进制，版本、Web 首页与 API 正常 |
| OpenWrt 服务适配 | procd 调用参数的自动化测试、OpenWrt 目录的暂存安装测试、对应架构交叉编译；尚未在路由器真机运行 |
| 更新逻辑 | 模拟 GitHub Release 的版本/架构选择、资源 URL、限流、缺失产物、SHA-256 失败、损坏下载、版本不符、启动失败恢复旧文件 |

## Linux 安装完整验收

验收脚本为 tests/integration/linux_install.py，使用本次发布脚本生成的实际二进制和部署包。仅下载层由 fixture 提供同样的 GitHub URL 对应资源，不访问未发布的线上 Release；安装、校验、文件写入、systemd、HTTP 与终端均执行真实代码。

通过项目：

1. 二进制和部署包损坏时拒绝安装，不修改目标路径。
2. 无效版本与不支持的架构参数被拒绝。
3. 自动识别 ARM64，从 latest 版本清单固定版本并安装。
4. miproxy 命令、程序版本、systemd 运行状态、开机启动、Web 首页、API 和本机 Socket 权限正确。
5. 通过真实终端导入、删除订阅。
6. 通过终端停止/启动服务，操作标签随状态切换；停止后台后终端仍可用。
7. 通过终端关闭/开启开机启动，退出终端后后台继续运行。
8. 指定版本重复安装保留已有环境文件和配置。
9. 通过终端卸载，移除服务与命令，保留配置。
10. --no-start 安装后服务保持停止，随后可从终端启动。

原生 ARM64 环境完整通过。amd64 systemd 在 ARM 宿主的仿真容器中未能启动，故没有将该环境记为 systemd 安装验收通过；GitHub 原生 amd64 Ubuntu runner 配置了同一验收脚本，本次 v0.1.0 已通过该原生 amd64 安装验收，链接见上方发布记录。

## 尚未完成

- 在新设备上通过实际 GitHub Release 在线安装的完整验收：当前真实安装验收的下载层仍为 fixture；正式资源的 HTTPS 下载与 SHA-256 已单独通过。首个版本无法覆盖真实旧版本到新版本的升级。
- OpenWrt 路由器真机、MIPS / ARM 老设备、不同发行版与固件兼容性。
- 物理设备重启后的自动启动、透明代理和实际路由接管。
- 用户截图中的 macOS Terminal 原生窗口显示偏移：伪终端的全屏控制与重绘测试通过，但原生窗口症状未复现。

正式发布步骤和补验方式见 [GitHub 打包与发布](releasing.md)。

## v0.1.1 验证与发布

- 后端改为 cmd / internal，前端按 features / shared / styles 组织；Go 竞态测试、Go vet、前端类型构建与 6 项 YAML 测试通过。
- 原四套浏览器回归通过，新增访问设置回归通过，覆盖保存、清除确认、密码校验和认证过期提示；浏览器 API 使用模拟数据。
- 13 个目标本地交叉编译完成，18 项 SHA-256 校验通过，macOS ARM64 版本号正确。
- 编译程序的真实 HTTP / 私有 Socket 集成测试通过：内嵌 Web、废弃密码环境变量不生效、密码摘要持久化、Basic Auth 以及本机恢复访问；真实伪终端执行 access 命令，确认隐藏输入不回显且新密码即时生效。
- Docker Desktop 的独立 ARM64 Debian systemd 容器中执行 tests/integration/linux_install.py，完整安装、终端导入删除、服务启停、自启动切换、重复安装保留配置、卸载和 --no-start 均通过；下载层仍为 fixture。测试容器完成后删除，不更改宿主服务。
- 体积数据见 package-size.md，截图来自独立演示数据与真实终端程序。[正式发布运行 45776953463](https://github.com/MaoLikeQvQ/MiProxy-Panel/actions/runs/45776953463) 已成功，完成原生 amd64 Ubuntu 的全部检查、真实编译程序认证及 systemd 安装验收。OpenWrt 真机验收仍待完成。

- v0.1.1 发布代码提交为 b63742c，标签指向该提交。[正式 Release](https://github.com/MaoLikeQvQ/MiProxy-Panel/releases/tag/v0.1.1) 已公开并设为 latest，包含 19 个文件；从 GitHub 实际下载全部文件后，校验清单中的 18 项均通过。
- 下载的 macOS ARM64 正式二进制返回 MiProxy Panel v0.1.1；使用该程序执行 tests/integration/runtime.py，通过真实 HTTP 访问认证、私有 Socket 恢复及真实伪终端密码隐藏输入测试，未改动已安装服务或用户配置。
