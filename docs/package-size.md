# 安装包体积实测

测量对象：v0.1.1 本地交叉编译产物（当前工作区，不等同于已发布版本）。二进制使用 CGO_ENABLED=0、-trimpath、-s -w，内嵌 Web 和终端。单位 1 MiB = 1,048,576 字节。

| 文件 | 实际下载字节 | 二进制 / 下载大小 | gzip -9 估算 |
| --- | ---: | ---: | ---: |
| miproxy-panel_darwin_amd64 | 10,269,088 | 9.79 MiB | 3.86 MiB |
| miproxy-panel_darwin_arm64 | 9,610,050 | 9.16 MiB | 3.58 MiB |
| miproxy-panel_linux_386 | 9,687,202 | 9.24 MiB | 3.70 MiB |
| miproxy-panel_linux_amd64 | 10,076,322 | 9.61 MiB | 3.81 MiB |
| miproxy-panel_linux_arm64 | 9,437,346 | 9.00 MiB | 3.46 MiB |
| miproxy-panel_linux_armv5 | 9,765,026 | 9.31 MiB | 3.67 MiB |
| miproxy-panel_linux_armv6 | 9,699,490 | 9.25 MiB | 3.66 MiB |
| miproxy-panel_linux_armv7 | 9,699,490 | 9.25 MiB | 3.65 MiB |
| miproxy-panel_linux_mips64 | 11,010,210 | 10.50 MiB | 3.41 MiB |
| miproxy-panel_linux_mips64le | 11,010,210 | 10.50 MiB | 3.39 MiB |
| miproxy-panel_linux_mips_softfloat | 10,879,169 | 10.38 MiB | 3.55 MiB |
| miproxy-panel_linux_mipsle_softfloat | 10,813,633 | 10.31 MiB | 3.52 MiB |
| miproxy-panel_linux_riscv64 | 9,109,666 | 8.69 MiB | 3.58 MiB |

全部 19 个发布文件合计 **125.02 MiB**。这是全部架构一起下载的总量；单台设备只需一个对应架构二进制及配套文件。

部署配套包 deploy.tar.gz：3,083 字节；在线安装脚本：6,370 字节。

gzip 列仅用于观察可压缩程度，正式发布的二进制文件本身没有 gzip 包装。二进制大小近似为面板可执行文件的安装占用；配置、订阅、规则缓存、日志、Mihomo 内核及 Geo 数据另计。

面板安装包不包含 Mihomo。接入已有内核时无需重复下载；新装内核时，内核由官方版本和平台决定体积，GeoIP / GeoSite 数据又取决于订阅需求，因此“面板 + 内核 + 所有数据”没有固定总大小。上述实测不包含 node_modules、Go 构建缓存或所有源码。

复测：make sizes OUTPUT=artifacts/release/v0.1.1。

## 当前本机内核示例

只读测量 本机 Sparkle 的 Mihomo v1.19.33 可执行文件：56,810,034 字节（54.18 MiB）。加上本次 miproxy-panel_darwin_arm64 9.16 MiB，两个可执行文件合计约 **63.34 MiB**。这是安装后的程序文件示例，不是一个合并安装包，也不包含 Sparkle 应用、Geo 数据、订阅缓存或日志，不能套用到所有平台和版本。
