# GitHub 打包与发布

正式版本在 GitHub Actions 的 Ubuntu runner 上构建。工作流为 .github/workflows/release.yml，发布脚本为 scripts/release/build.sh。目标设备只下载安装产物，不编译源码。

## 预检

代码推送到仓库后，在 GitHub 的 Actions → Release → Run workflow 填写预检版本，例如 v0.1.0。

也可以使用 GitHub CLI：

~~~sh
gh workflow run release.yml --ref main -f version=v0.1.0
gh run list --workflow release.yml
~~~

手动预检执行完整测试、打包、Linux 安装验收，并上传 Actions Artifact；不会创建 Release。初始仓库为空时，需要先提交并推送源码和工作流，才能运行预检。

## 正式发布

确认预检通过后，将准备发布的提交打上正式版本标签并推送，例如：

~~~sh
git tag v0.1.0
git push origin v0.1.0
~~~

仅接受 v数字.数字.数字 格式，不发布草稿版本或预发布后缀。不要重复使用已发布的标签。

工作流顺序：

1. 使用 Go 1.26 和 Node.js 24，按锁文件安装前端依赖。
2. 构建 Web 静态资源，执行类型检查、Go 竞态测试、Go vet、YAML 测试和 ShellCheck，并在 Chromium 中执行内核管理、检测接入、订阅应用、统一规则列表和访问设置五套浏览器回归。
3. 交叉编译 13 个目标，将标签写入 github.com/MaoLikeQvQ/MiProxy-Panel/internal/buildinfo.Version。
4. 在可丢弃的 Ubuntu runner 上以 root 执行真实 systemd 安装验收。测试用本次产物模拟 Release 下载，运行生产安装脚本，覆盖校验失败、默认安装、指定版本、重复安装、终端导入删除、服务启停、自启动切换、卸载及 --no-start。
5. 保存 Actions Artifact。标签触发的运行创建草稿 Release，上传全部文件后才公开并设为 latest；任一前置检查失败不会发布。

Release 使用 GitHub 自动提供的 GITHUB_TOKEN，写权限限制在发布 job 的 contents: write，不需要额外配置个人 token。

## 产物约定

| 文件 | 用途 |
| --- | --- |
| miproxy-panel_<系统>_<架构> | 带 Web 静态资源和终端界面的独立二进制 |
| install.sh | Linux / OpenWrt 直接安装入口 |
| deploy.tar.gz | 离线安装脚本、systemd/procd 服务与环境模板 |
| version.txt | 固定 latest 安装所用的正式版本 |
| checksums.txt | 其他全部发布资源的 SHA-256 |
| downloads.md | 所有文件、13 个目标架构与适用环境对照 |
| usage.md | 可离线阅读的完整使用说明 |

Linux 架构：amd64、arm64、386、armv5、armv6、armv7、mips_softfloat、mipsle_softfloat、mips64、mips64le、riscv64。macOS 架构：amd64、arm64。所有二进制使用 CGO_ENABLED=0。完整版本共 19 个发布文件，其中 18 个文件列入 SHA-256 清单，清单自身不重复校验。文件选择详见[下载说明](downloads.md)。

版本说明保存在 `docs/releases/<版本>.md`，发布时优先作为 Release 正文；没有对应文件时才生成 GitHub 自动说明。

终端更新从本仓库 /releases/latest 读取版本，自动匹配文件；不直接拉取 main 分支。首次安装通过 GitHub latest download 的 version.txt 固定版本，然后从同一标签下载二进制、部署包和校验清单，避免 latest 变动导致混用版本。

## 发布后验收

用可丢弃的 Linux 设备或虚拟机执行 README 在线安装命令，确认实际 GitHub Release 的 HTTPS 下载成功。本地下载 fixture 验收不替代此项。

- miproxy --version 与发布标签一致。
- systemctl is-active miproxy-panel 和 is-enabled 符合预期。
- 浏览器通过本机或 SSH 转发访问 Web，终端可以打开并管理服务。
- 已安装上一正式版本的设备可在终端检查并更新至新版。
- 验证 Release 对应架构文件、部署包和 SHA-256 全部可下载。

OpenWrt 实际固件与硬件兼容性需要单独验收，不能用 Linux 交叉编译或模拟 procd 命令代替。

## 本地预检

本地构建只用于开发和复现，正式产物以 GitHub runner 为准：

~~~sh
npm --prefix web ci
npm --prefix web run build
go test -race ./...
go vet ./...
node --test web/tests/unit/yaml.test.mjs
shellcheck install.sh deploy/install.sh scripts/release/build.sh
actionlint .github/workflows/release.yml
sh scripts/release/build.sh v0.1.0 /绝对路径/空输出目录
~~~

安装验收脚本会更改系统服务，只能在可丢弃的 root Linux 环境运行，并拒绝覆盖已有安装：

~~~sh
sudo env MIPROXY_TEST_ISOLATED=1 python3 tests/integration/linux_install.py --release-dir /绝对路径/发布产物目录
~~~
