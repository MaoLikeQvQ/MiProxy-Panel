# 下载文件与适用环境

从 [GitHub Releases](https://github.com/MaoLikeQvQ/MiProxy-Panel/releases) 下载同一版本的文件。Linux / OpenWrt 在线安装器会自动选择架构，通常无需手动选包。需要离线安装或在 macOS 运行时，按下表选择。

## 二进制文件

每个文件都是可执行程序，包含 Web 页面和终端界面，不包含 Mihomo 内核。运行不依赖 Node.js、Go 或系统 C 运行库（构建使用 `CGO_ENABLED=0`）。设备仍需具备对应 CPU 指令集和操作系统支持；交叉编译成功不等于对应硬件真机验收。

| 文件名 | 系统与架构 | 适合的环境 |
| --- | --- | --- |
| `miproxy-panel_linux_amd64` | Linux x86-64，AMD64 / Intel 64 | 常见 Intel / AMD 64 位服务器、x86 软路由、Linux NAS、Linux 虚拟机 |
| `miproxy-panel_linux_arm64` | Linux ARM64 / AArch64 | ARM64 服务器、64 位 ARM 软路由、运行 64 位系统的树莓派及 ARM NAS |
| `miproxy-panel_linux_386` | Linux 32 位 x86 | 运行 32 位 Linux 的旧 x86 设备；不适用于 ARM |
| `miproxy-panel_linux_armv5` | Linux 32 位 ARMv5 | ARMv5 及对应兼容的旧 ARM Linux / 路由设备 |
| `miproxy-panel_linux_armv6` | Linux 32 位 ARMv6 | 采用 ARMv6 用户空间的设备，例如运行相应 32 位系统的早期树莓派 |
| `miproxy-panel_linux_armv7` | Linux 32 位 ARMv7 | ARMv7 路由器、单板机、运行 32 位 ARMv7 系统的设备 |
| `miproxy-panel_linux_mips_softfloat` | Linux 32 位 MIPS，大端、软件浮点 | 使用大端 MIPS 用户空间的 OpenWrt / 嵌入式 Linux；不能与 mipsle 混用 |
| `miproxy-panel_linux_mipsle_softfloat` | Linux 32 位 MIPS，小端、软件浮点 | 使用小端 MIPS 用户空间的 OpenWrt / 嵌入式 Linux；常见平台命名为 mipsel |
| `miproxy-panel_linux_mips64` | Linux 64 位 MIPS，大端，Go 默认硬浮点 | 使用对应 64 位大端用户空间的 MIPS64 设备 |
| `miproxy-panel_linux_mips64le` | Linux 64 位 MIPS，小端，Go 默认硬浮点 | 使用对应 64 位小端用户空间的 MIPS64 设备 |
| `miproxy-panel_linux_riscv64` | Linux 64 位 RISC-V | 运行 64 位 RISC-V Linux 的开发板或服务器 |
| `miproxy-panel_darwin_amd64` | macOS Intel x86-64 | Intel Mac，手动运行 Web 服务与终端；不提供 macOS 服务安装器 |
| `miproxy-panel_darwin_arm64` | macOS Apple Silicon ARM64 | Apple M 系列 Mac，手动运行 Web 服务与终端；不提供 macOS 服务安装器 |

先在目标设备运行 `uname -s` 和 `uname -m`。常见对应关系：`x86_64` → `amd64`，`aarch64` → `arm64`，`armv7l` → `armv7`，`armv6l` → `armv6`，`i686` → `386`。64 位 CPU 运行 32 位操作系统时应按用户空间选择，不能只看 CPU 型号。OpenWrt 还可以运行 `ubus call system board` 查看目标平台；MIPS 必须确认字节序。32 位 ARMv8 用户空间若报告 `armv8l`，确认兼容 ARMv7 后可用安装器 `--arch armv7`。无法确认时先使用在线安装器自动检测，或核对固件的架构说明。

`linux_*` 文件可用于对应架构的 OpenWrt，但自动服务安装仅适配 procd；普通 Linux 自动服务安装需要正在运行的 systemd。Alpine / OpenRC、精简容器或其他 NAS 服务框架需要自行管理进程，或使用仓库的 Docker Compose。Go 运行时对系统内核和 CPU 的要求仍然适用，旧固件需在对应设备验证。

## 配套文件

| 文件名 | 适用环境与用途 |
| --- | --- |
| `install.sh` | Linux systemd / OpenWrt procd 的在线安装入口，需要 root、curl、CA 证书、tar 和 SHA-256 工具；从本 Release 下载正确的二进制与部署包 |
| `deploy.tar.gz` | Linux / OpenWrt 离线安装配套包，含 `deploy/install.sh`、systemd / procd 服务模板、环境变量模板；必须搭配同一版本的目标二进制 |
| `version.txt` | 安装器解析的正式版本号，用于固定 latest 对应的版本；不需要运行 |
| `checksums.txt` | 所有其他发布文件的 SHA-256 清单，用于校验下载是否完整；自身不列入校验清单 |
| `downloads.md` | 本文件，解释每个发布文件适合的系统与设备 |
| `usage.md` | 可离线阅读的使用说明，覆盖首次启动、订阅、规则、节点、更新与常见问题 |

GitHub 自动生成的 `Source code (zip)` / `Source code (tar.gz)` 是源码，不是已编译的安装包。当前没有 Windows、macOS `.app` / `.dmg`、Linux `.deb` / `.rpm`、OpenWrt `.ipk` / `.apk` 或发布到镜像仓库的容器镜像。

## 校验与安装

下载同一 Release 的目标文件和 `checksums.txt`，在包含下载文件的目录执行。例如 Linux ARM64 离线安装：

~~~sh
awk '$2 == "miproxy-panel_linux_arm64" || $2 == "deploy.tar.gz"' checksums.txt | sha256sum -c -
tar -xzf deploy.tar.gz
chmod +x miproxy-panel_linux_arm64
sudo sh deploy/install.sh --binary "$PWD/miproxy-panel_linux_arm64"
sudo miproxy
~~~

macOS 使用 `shasum -a 256` 校验，按 `usage.md` 中的 macOS 步骤运行。不要在 macOS 上执行 Linux 服务安装脚本。

## 验证范围

Linux amd64 的发布工作流会在 Ubuntu runner 中执行真实 systemd 安装验收。macOS、Linux ARM64 的本地验证记录见仓库的 `docs/testing.md`。其余架构提供交叉编译产物，OpenWrt、MIPS 和旧 ARM 设备仍需对应硬件与固件验收。面板启动内核不会自动设置系统代理或透明代理，也不会自动接管路由器流量。
