#!/bin/sh
set -eu

usage() {
    cat <<'EOF'
用法：sh deploy/install.sh --binary /path/to/miproxy-panel [--no-start]

将已编译的面板安装为系统服务，并提供 miproxy 终端命令。
已有配置、环境文件和服务定义会保留。支持 Linux systemd / OpenWrt。

  --binary PATH     必填，目标设备可执行的面板二进制
  --no-start        安装但不启动服务
  --system SYSTEM   指定 systemd 或 openwrt（通常自动检测）
  --help            显示帮助

DESTDIR 可用于打包到暂存目录；暂存安装不启动或启用宿主服务。
EOF
}

fail() { printf '%s\n' "$*" >&2; exit 1; }

binary_source=
system=
start_service=1
while [ "$#" -gt 0 ]; do
    case "$1" in
        --binary) [ "$#" -ge 2 ] || fail '--binary 缺少路径'; binary_source=$2; shift 2 ;;
        --system) [ "$#" -ge 2 ] || fail '--system 缺少类型'; system=$2; shift 2 ;;
        --no-start) start_service=0; shift ;;
        --help|-h) usage; exit 0 ;;
        *) fail "未知参数：$1" ;;
    esac
done
[ -n "$binary_source" ] || { usage >&2; exit 1; }
[ -f "$binary_source" ] && [ -x "$binary_source" ] || fail '二进制文件不存在或不可执行'
install_root=${DESTDIR:-}
case "$install_root" in
    '') [ "$(id -u)" = 0 ] || fail '安装需要 root 权限，请以 root 身份执行此脚本' ;;
    /*) install_root=${install_root%/}; [ -n "$install_root" ] || fail 'DESTDIR 不能为根目录'; start_service=0 ;;
    *) fail 'DESTDIR 必须是绝对路径' ;;
esac
if [ -z "$install_root" ]; then
    binary_version=$("$binary_source" --version) || fail '二进制无法在当前设备运行，安装已取消'
    case "$binary_version" in 'MiProxy Panel '*) ;; *) fail '二进制不是 MiProxy Panel，安装已取消' ;; esac
fi

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
if [ -z "$system" ]; then
    if [ -f "$install_root/etc/openwrt_release" ]; then
        system=openwrt
    elif [ "$(uname -s)" = Linux ] && command -v systemctl >/dev/null 2>&1; then
        system=systemd
    else
        fail '未检测到 Linux systemd 或 OpenWrt，请使用对应平台的部署方式'
    fi
fi
case "$system" in
    systemd)
        manager=systemd
        binary_target=/usr/local/bin/miproxy-panel
        service_target=/etc/systemd/system/miproxy-panel.service
        template=$script_dir/miproxy-panel.service
        core_binary=/usr/local/bin/mihomo
        ;;
    openwrt)
        manager=procd
        binary_target=/usr/bin/miproxy-panel
        service_target=/etc/init.d/miproxy-panel
        template=$script_dir/miproxy-panel.openwrt
        core_binary=/usr/bin/mihomo
        ;;
    *) fail '--system 只能为 systemd 或 openwrt' ;;
esac
[ -f "$template" ] || fail "缺少服务模板：$template"
command_target=${binary_target%-panel}
for target in "$binary_target" "$service_target" /etc/miproxy-panel.env /etc/miproxy-panel.install; do
    [ ! -L "$install_root$target" ] || fail "安装路径不能是符号链接：$target"
    [ ! -e "$install_root$target" ] || [ -f "$install_root$target" ] || fail "安装路径不是普通文件：$target"
done
if [ -e "$install_root$command_target" ] || [ -L "$install_root$command_target" ]; then
    [ -L "$install_root$command_target" ] && [ "$(readlink "$install_root$command_target")" = "$binary_target" ] ||
        fail "已有其他命令占用 $command_target，请先检查"
fi
if [ -e "$install_root$service_target" ]; then
    case "$manager" in
        systemd) expected="ExecStart=$binary_target" ;;
        procd) expected="procd_set_param command $binary_target" ;;
    esac
    grep -F "$expected" "$install_root$service_target" >/dev/null || fail '已有服务使用其他二进制路径，请保留原部署方式'
fi

umask 022
mkdir -p "$install_root$(dirname "$binary_target")" "$install_root$(dirname "$service_target")"
if [ ! -d "$install_root/etc/mihomo" ]; then
    mkdir -p "$install_root/etc/mihomo"
    chmod 700 "$install_root/etc/mihomo"
fi
binary_temp=
env_temp=
marker_temp=
trap 'rm -f ${binary_temp:+"$binary_temp"} ${env_temp:+"$env_temp"} ${marker_temp:+"$marker_temp"}' EXIT HUP INT TERM
binary_temp=$(mktemp "$install_root$binary_target.XXXXXX")
cp "$binary_source" "$binary_temp"
chmod 755 "$binary_temp"
mv -f "$binary_temp" "$install_root$binary_target"
binary_temp=
if [ ! -e "$install_root$command_target" ] && [ ! -L "$install_root$command_target" ]; then
    ln -s "$binary_target" "$install_root$command_target"
fi
if [ ! -e "$install_root$service_target" ]; then
    cp "$template" "$install_root$service_target"
    if [ "$manager" = procd ]; then chmod 755 "$install_root$service_target"; else chmod 644 "$install_root$service_target"; fi
fi
if [ ! -e "$install_root/etc/miproxy-panel.env" ]; then
    umask 077
    env_temp=$(mktemp "$install_root/etc/miproxy-panel.env.XXXXXX")
    cat >"$env_temp" <<EOF
PANEL_LISTEN=127.0.0.1:8787
PANEL_CONFIG=/etc/mihomo/config.yaml
MIHOMO_BINARY=$core_binary
MIHOMO_HOME=/etc/mihomo
MIHOMO_CONTROLLER=http://127.0.0.1:9090
EOF
    chmod 600 "$env_temp"
    mv "$env_temp" "$install_root/etc/miproxy-panel.env"
    env_temp=
fi
umask 077
marker_temp=$(mktemp "$install_root/etc/miproxy-panel.install.XXXXXX")
printf 'miproxy-panel-install-v1\nmanager=%s\nbinary=%s\n' "$manager" "$binary_target" >"$marker_temp"
chmod 600 "$marker_temp"
mv -f "$marker_temp" "$install_root/etc/miproxy-panel.install"
marker_temp=

if [ -z "$install_root" ]; then
    if [ "$manager" = systemd ]; then
        systemctl daemon-reload
        systemctl enable miproxy-panel.service
        if [ "$start_service" = 1 ]; then
            systemctl restart miproxy-panel.service
            systemctl is-active --quiet miproxy-panel.service || fail '面板服务未启动成功，请运行 journalctl -u miproxy-panel.service 查看原因'
        fi
    else
        /etc/init.d/miproxy-panel enable
        if [ "$start_service" = 1 ]; then
            /etc/init.d/miproxy-panel restart
            attempts=0
            until /etc/init.d/miproxy-panel running; do
                attempts=$((attempts + 1))
                [ "$attempts" -lt 10 ] || fail '面板服务未启动成功，请运行 logread -e miproxy-panel 查看原因'
                sleep 1
            done
        fi
    fi
fi
printf '\n%s\n' '安装完成。输入 miproxy 打开终端面板。'
if [ -n "$install_root" ]; then
    printf '暂存目录：%s（未修改宿主服务）\n' "$install_root"
elif [ "$start_service" = 0 ]; then
    printf '%s\n' '服务尚未启动，可从终端面板的「服务管理」启动。'
else
    printf '%s\n' 'Web 面板默认地址：http://127.0.0.1:8787；已有环境文件按原设置运行。'
fi
