#!/usr/bin/env python3
"""Measure actual release files; gzip is an estimate, never a published archive."""
import argparse
import gzip
from pathlib import Path
parser=argparse.ArgumentParser()
parser.add_argument('--release-dir',required=True)
parser.add_argument('--output')
parser.add_argument('--core-label', default='Mihomo（指定文件）')
parser.add_argument('--panel-asset', default='miproxy-panel_darwin_arm64')
parser.add_argument('--core-binary',help='Optional existing Mihomo executable for a concrete installed-size example')
args=parser.parse_args()
folder=Path(args.release_dir)
binaries=sorted(folder.glob('miproxy-panel_*'))
if not binaries: raise SystemExit('没有可测量的二进制')
version=(folder/'version.txt').read_text().strip()
files=[p for p in folder.iterdir() if p.is_file()]
def mib(value):return f'{value/1048576:.2f} MiB'
lines=['# 安装包体积实测','',f'测量对象：{version} 本地交叉编译产物（当前工作区，不等同于已发布版本）。二进制使用 CGO_ENABLED=0、-trimpath、-s -w，内嵌 Web 和终端。单位 1 MiB = 1,048,576 字节。','', '| 文件 | 实际下载字节 | 二进制 / 下载大小 | gzip -9 估算 |','| --- | ---: | ---: | ---: |']
for file in binaries:
    data=file.read_bytes()
    lines.append(f'| {file.name} | {len(data):,} | {mib(len(data))} | {mib(len(gzip.compress(data,compresslevel=9,mtime=0)))} |')
size=sum(p.stat().st_size for p in files)
lines.extend(['',f'全部 {len(files)} 个发布文件合计 **{mib(size)}**。这是全部架构一起下载的总量；单台设备只需一个对应架构二进制及配套文件。', '',f'部署配套包 deploy.tar.gz：{(folder/"deploy.tar.gz").stat().st_size:,} 字节；在线安装脚本：{(folder/"install.sh").stat().st_size:,} 字节。', '', 'gzip 列仅用于观察可压缩程度，正式发布的二进制文件本身没有 gzip 包装。二进制大小近似为面板可执行文件的安装占用；配置、订阅、规则缓存、日志、Mihomo 内核及 Geo 数据另计。', '', '面板安装包不包含 Mihomo。接入已有内核时无需重复下载；新装内核时，内核由官方版本和平台决定体积，GeoIP / GeoSite 数据又取决于订阅需求，因此“面板 + 内核 + 所有数据”没有固定总大小。上述实测不包含 node_modules、Go 构建缓存或所有源码。', '', '复测：make sizes OUTPUT=artifacts/release/v0.1.1。'])
if args.core_binary:
    core=Path(args.core_binary)
    core_size=core.stat().st_size
    panel=folder/args.panel_asset
    lines.extend(['', '## 当前本机内核示例', '', f'只读测量 {args.core_label} 可执行文件：{core_size:,} 字节（{mib(core_size)}）。加上本次 {args.panel_asset} {mib(panel.stat().st_size)}，两个可执行文件合计约 **{mib(core_size+panel.stat().st_size)}**。这是安装后的程序文件示例，不是一个合并安装包，也不包含 Sparkle 应用、Geo 数据、订阅缓存或日志，不能套用到所有平台和版本。'])
result=chr(10).join(lines)+chr(10)
if args.output:Path(args.output).write_text(result)
else:print(result)
