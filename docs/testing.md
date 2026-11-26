# 第一版验收记录

记录日期：2026-11-26。以下记录本地工作区和发布前的验证范围；云端运行与正式产物以 [Actions](https://github.com/MaoLikeQvQ/MiProxy-Panel/actions) 和 [Releases](https://github.com/MaoLikeQvQ/MiProxy-Panel/releases) 的实际记录为准。

## 已通过

| 项目 | 验证方式与范围 |
| --- | --- |
| 前端类型与构建 | Node.js 24 下运行 npm --prefix web run build，包含 vue-tsc 类型检查 |
| 后端测试 | Go 1.26 下 go test -race ./... 与 go vet ./... |
| YAML 行为 | node --test web/test/yaml.test.mjs，6 项通过，覆盖订阅合并、规则、覆写与地理数据设置 |
| Web 浏览器回归 | core-dialog、discovery-ui、runtime-ui、unified-rules 四套脚本；覆盖内核对话框、已有内核选择、移动端暗色布局、日志连接条件、下载进度、订阅应用及失败提示、统一规则页添加/编辑/删除撤销、规则集地址与策略写入、外部草稿导出；API 使用测试数据 |
| 终端 | 真实伪终端中输入与菜单操作，窗口缩放、订阅导入/删除/应用、节点切换与日志；不等同于 macOS Terminal 的全部绘制行为 |
| 发布脚本 | 本地构建 13 个目标，校验二进制及配套文件的 SHA-256、版本号、部署包路径；本次新增可离线阅读的 downloads.md / usage.md；修复 macOS tar 扩展元数据导致 Linux 拒绝部署包的问题 |
| Shell 静态检查 | install.sh、deploy/install.sh、deploy/build-release.sh 的 ShellCheck 与 sh -n |
| GitHub 工作流静态检查 | actionlint；正式发布前必须经过测试和安装验收，手动运行只保留 Artifact |
| Linux ARM64 原生安装 | Docker Desktop Linux VM 内的可丢弃 Debian 13 ARM64 容器，PID 1 为真实 systemd；安装目录和服务均为真实路径，未修改 macOS 宿主系统 |
| Linux amd64 运行 | 仿真容器中启动实际 amd64 发布二进制，版本、Web 首页与 API 正常 |
| OpenWrt 服务适配 | procd 调用参数的自动化测试、OpenWrt 目录的暂存安装测试、对应架构交叉编译；尚未在路由器真机运行 |
| 更新逻辑 | 模拟 GitHub Release 的版本/架构选择、资源 URL、限流、缺失产物、SHA-256 失败、损坏下载、版本不符、启动失败恢复旧文件 |

## Linux 安装完整验收

验收脚本为 deploy/test-linux.py，使用本次发布脚本生成的实际二进制和部署包。仅下载层由 fixture 提供同样的 GitHub URL 对应资源，不访问未发布的线上 Release；安装、校验、文件写入、systemd、HTTP 与终端均执行真实代码。

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

原生 ARM64 环境完整通过。amd64 systemd 在 ARM 宿主的仿真容器中未能启动，故没有将该环境记为 systemd 安装验收通过；GitHub 原生 amd64 Ubuntu runner 配置了同一验收脚本，实际结果见发布运行记录。

## 尚未完成

- 实际 GitHub Release 的 HTTPS 下载和后续直接安装需在发布后补验；首个版本无法覆盖真实旧版本到新版本的升级。
- OpenWrt 路由器真机、MIPS / ARM 老设备、不同发行版与固件兼容性。
- 物理设备重启后的自动启动、透明代理和实际路由接管。
- 用户截图中的 macOS Terminal 原生窗口显示偏移：伪终端的全屏控制与重绘测试通过，但原生窗口症状未复现。

正式发布步骤和补验方式见 [GitHub 打包与发布](releasing.md)。
