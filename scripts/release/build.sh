#!/bin/sh
set -eu

release_version=${1:-}
release_output=${2:-dist-release}
if ! printf '%s\n' "$release_version" | LC_ALL=C grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
    printf '%s\n' '用法：sh scripts/release/build.sh v1.2.3 [输出目录]' >&2
    exit 1
fi
project_root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$project_root"
mkdir -p "$release_output"
release_output=$(CDPATH='' cd -- "$release_output" && pwd)
# A dedicated empty directory prevents stale binaries from entering a release.
[ -z "$(ls -A "$release_output")" ] || { printf '%s\n' '发布输出目录必须为空' >&2; exit 1; }
[ -f web/dist/index.html ] || { printf '%s\n' '请先构建 web/dist' >&2; exit 1; }
release_targets=${RELEASE_TARGETS:-'linux_amd64 linux_arm64 linux_386 linux_armv5 linux_armv6 linux_armv7 linux_mips_softfloat linux_mipsle_softfloat linux_mips64 linux_mips64le linux_riscv64 darwin_amd64 darwin_arm64'}
for target in $release_targets; do
    system=${target%%_*}
    architecture=${target#*_}
    arm=7
    case "$architecture" in
        armv5|armv6|armv7) arm=${architecture#armv}; architecture=arm ;;
        mips_softfloat) architecture=mips ;;
        mipsle_softfloat) architecture=mipsle ;;
    esac
    case "$target" in
        linux_amd64|linux_arm64|linux_386|linux_armv5|linux_armv6|linux_armv7|linux_mips_softfloat|linux_mipsle_softfloat|linux_mips64|linux_mips64le|linux_riscv64|darwin_amd64|darwin_arm64) ;;
        *) printf '不支持的发布目标：%s\n' "$target" >&2; exit 1 ;;
    esac
    printf '构建 %s %s\n' "$release_version" "$target"
    CGO_ENABLED=0 GOOS="$system" GOARCH="$architecture" GOARM="$arm" GOMIPS=softfloat \
        go build -trimpath -ldflags="-s -w -X github.com/MaoLikeQvQ/MiProxy-Panel/internal/buildinfo.Version=$release_version" \
        -o "$release_output/miproxy-panel_$target" ./cmd/miproxy-panel
done
COPYFILE_DISABLE=1 tar --format=ustar -czf "$release_output/deploy.tar.gz" deploy/install.sh deploy/miproxy-panel.service deploy/miproxy-panel.openwrt deploy/panel.env.example
cp install.sh "$release_output/install.sh"
cp docs/downloads.md "$release_output/downloads.md"
cp docs/usage.md "$release_output/usage.md"
printf '%s\n' "$release_version" >"$release_output/version.txt"
cd "$release_output"
if command -v sha256sum >/dev/null 2>&1; then
    sha256sum miproxy-panel_* deploy.tar.gz install.sh version.txt downloads.md usage.md >checksums.txt
else
    shasum -a 256 miproxy-panel_* deploy.tar.gz install.sh version.txt downloads.md usage.md >checksums.txt
fi
printf '发布文件已生成：%s\n' "$release_output"
