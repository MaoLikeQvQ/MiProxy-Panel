#!/bin/sh
set -eu

repository=MaoLikeQvQ/MiProxy-Panel
release_version=latest
requested_arch=
no_start=

fail() { printf '%s\n' "$*" >&2; exit 1; }
usage() {
    cat <<'EOF'
用法：sh install.sh [--version v1.0.0] [--arch armv7] [--no-start]

从 MaoLikeQvQ/MiProxy-Panel GitHub Release 下载并校验，安装 miproxy 命令与服务。
支持 Linux systemd 和 OpenWrt procd。默认安装最新正式版。
--version VERSION  安装指定正式版本
--arch ARCH        指定架构；通常自动检测，旧路由器可手动指定
--no-start         安装并开启开机启动，当前不启动服务
--help            显示帮助
EOF
}
valid_version() { printf '%s\n' "$1" | LC_ALL=C grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; }
while [ "$#" -gt 0 ]; do
    case "$1" in
        --version) [ "$#" -ge 2 ] || fail '--version 缺少版本号'; release_version=$2; shift 2 ;;
        --arch) [ "$#" -ge 2 ] || fail '--arch 缺少架构'; requested_arch=$2; shift 2 ;;
        --no-start) no_start=--no-start; shift ;;
        --help|-h) usage; exit 0 ;;
        *) fail "未知参数：$1" ;;
    esac
done
[ "$release_version" = latest ] || valid_version "$release_version" || fail '版本号必须为 v主版本.次版本.修订版本'
[ "$(uname -s)" = Linux ] || fail '直接安装支持 Linux / OpenWrt；macOS 请使用开发运行方式'
[ "$(id -u)" = 0 ] || fail '安装需要 root 权限，请使用 sudo sh install.sh 或在 root SSH 中运行'
if [ -f /etc/openwrt_release ]; then
    install_system=openwrt
elif command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    install_system=systemd
else
    fail '未发现正在运行的 systemd 或 OpenWrt procd；请在完整 Linux 系统上安装'
fi
for dependency in tar mktemp awk; do command -v "$dependency" >/dev/null 2>&1 || fail "缺少工具：$dependency"; done
if command -v sha256sum >/dev/null 2>&1; then
    checksum_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then
    checksum_tool=shasum
else
    fail '缺少 SHA-256 工具，请安装 sha256sum'
fi
command -v curl >/dev/null 2>&1 || fail '缺少 curl；Debian/Ubuntu 请安装 curl ca-certificates，OpenWrt 请安装 curl ca-bundle'
architecture=$requested_arch
if [ -z "$architecture" ]; then
    machine=$(uname -m)
    case "$machine" in
        x86_64|amd64) architecture=amd64 ;;
        aarch64|arm64) architecture=arm64 ;;
        i386|i486|i586|i686) architecture=386 ;;
        armv7*) architecture=armv7 ;;
        armv6*) architecture=armv6 ;;
        armv5*|arm) architecture=armv5 ;;
        riscv64) architecture=riscv64 ;;
        mips|mips64)
            command -v od >/dev/null 2>&1 || fail 'MIPS 自动检测需要 od，或使用 --arch 指定架构'
            byte_order=$(od -An -t u1 -j 5 -N 1 /proc/self/exe | tr -d ' ')
            [ "$byte_order" = 1 ] || [ "$byte_order" = 2 ] || fail '无法识别 MIPS 字节序，请使用 --arch'
            architecture=$machine
            if [ "$byte_order" = 1 ]; then architecture=${architecture}le; fi
            if [ "$machine" = mips ]; then architecture=${architecture}_softfloat; fi ;;
        mipsel|mipsle) architecture=mipsle_softfloat ;;
        mips64el|mips64le) architecture=mips64le ;;
        *) fail "不支持的架构：$machine；可用 --arch 明确指定发布架构" ;;
    esac
fi
case "$architecture" in
    amd64|arm64|386|armv5|armv6|armv7|mips_softfloat|mipsle_softfloat|mips64|mips64le|riscv64) ;;
    *) fail "不支持的发布架构：$architecture" ;;
esac
download_directory=$(mktemp -d)
trap 'rm -rf "$download_directory"' EXIT
trap 'exit 1' HUP INT TERM
download() {
    curl --proto '=https' --tlsv1.2 --fail --location --retry 2 --connect-timeout 15 --max-time 600 \
        --output "$2" "$1" || fail 'GitHub 下载失败；请检查网络、版本是否发布以及对应架构文件是否存在'
}
if [ "$release_version" = latest ]; then
    download "https://github.com/$repository/releases/latest/download/version.txt" "$download_directory/version.txt"
    release_version=$(cat "$download_directory/version.txt")
    valid_version "$release_version" || fail 'GitHub 返回的版本号无效，已停止安装'
fi
asset=miproxy-panel_linux_$architecture
release_base=https://github.com/$repository/releases/download/$release_version
printf '准备安装 MiProxy Panel %s（linux/%s）\n' "$release_version" "$architecture"
download "$release_base/checksums.txt" "$download_directory/checksums.txt"
for filename in "$asset" deploy.tar.gz; do
    download "$release_base/$filename" "$download_directory/$filename"
    expected=$(awk -v file="$filename" '$2 == file || $2 == "*" file {print $1}' "$download_directory/checksums.txt")
    [ "${#expected}" = 64 ] || fail "校验清单缺少唯一的 $filename 校验值"
    case "$expected" in *[!0-9a-fA-F]*) fail '校验清单无效' ;; esac
    if [ "$checksum_tool" = sha256sum ]; then
        actual=$(sha256sum "$download_directory/$filename" | awk '{print $1}')
    else
        actual=$(shasum -a 256 "$download_directory/$filename" | awk '{print $1}')
    fi
    [ "$actual" = "$expected" ] || fail "$filename SHA-256 校验失败，未修改安装"
done
# Reject archive paths outside the deployment directory before extracting scripts.
tar -tzf "$download_directory/deploy.tar.gz" >"$download_directory/archive-files"
while IFS= read -r entry; do
    case "$entry" in
        deploy/install.sh|deploy/miproxy-panel.service|deploy/miproxy-panel.openwrt|deploy/panel.env.example) ;;
        *) fail "部署包包含不支持的路径：$entry" ;;
    esac
done <"$download_directory/archive-files"
tar -xzf "$download_directory/deploy.tar.gz" -C "$download_directory"
for filename in install.sh miproxy-panel.service miproxy-panel.openwrt panel.env.example; do
    [ -f "$download_directory/deploy/$filename" ] && [ ! -L "$download_directory/deploy/$filename" ] || fail '部署文件包缺失或含符号链接'
done
chmod 755 "$download_directory/$asset"
detected_version=$("$download_directory/$asset" --version) || fail '下载的程序不能在当前设备运行，未修改安装'
[ "$detected_version" = "MiProxy Panel $release_version" ] || fail '二进制版本号不匹配，未修改安装'
sh "$download_directory/deploy/install.sh" --binary "$download_directory/$asset" --system "$install_system" ${no_start:+"$no_start"}
