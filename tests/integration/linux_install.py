#!/usr/bin/env python3
"""Destructive installation acceptance for a disposable systemd runner/container."""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import select
import shutil
import struct
import subprocess
import tempfile
import termios
import time
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument('--release-dir', required=True)
args = parser.parse_args()
if os.geteuid() != 0 or os.environ.get('MIPROXY_TEST_ISOLATED') != '1':
    raise SystemExit('仅在可丢弃 Linux 环境以 root 设置 MIPROXY_TEST_ISOLATED=1 执行')
if not Path('/run/systemd/system').is_dir():
    raise SystemExit('需要真实运行的 systemd')
if Path('/usr/local/bin/miproxy-panel').exists() or Path('/etc/miproxy-panel.install').exists():
    raise SystemExit('已有面板部署，拒绝覆盖测试')
root = Path(__file__).resolve().parents[2]
assets = Path(args.release_dir).resolve()
release_version = (assets / 'version.txt').read_text().strip()
env = os.environ.copy()
fixture = Path(tempfile.mkdtemp(prefix='miproxy-linux-acceptance-'))


def run(*cmd, check=True, **kwargs):
    result = subprocess.run(cmd, check=False, text=True, stdout=subprocess.PIPE,
                            stderr=subprocess.STDOUT, **kwargs)
    if check and result.returncode:
        raise AssertionError('命令失败：' + ' '.join(cmd) + '\n' + result.stdout)
    return result


def wait_api(online=True):
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen('http://127.0.0.1:8787/api/core', timeout=1) as response:
                if online:
                    return json.load(response)
        except (OSError, ValueError):
            if not online:
                return
        time.sleep(.15)
    raise AssertionError('面板 HTTP 状态不符合预期')


class Terminal:
    def __init__(self):
        self.master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 30, 117, 0, 0))
        terminal_env = env | {'TERM': 'xterm-256color', 'NO_COLOR': '1'}
        self.process = subprocess.Popen(['/usr/local/bin/miproxy'], stdin=slave, stdout=slave,
                                        stderr=slave, env=terminal_env, start_new_session=True)
        os.close(slave)
        self.data = b''

    def send(self, text):
        self.data = b''
        os.write(self.master, text.encode())

    def wait(self, text, timeout=15):
        end = time.monotonic() + timeout
        while time.monotonic() < end:
            if text.encode() in self.data:
                return
            if select.select([self.master], [], [], .1)[0]:
                block = os.read(self.master, 65536)
                self.data += block
                if b'\x1b]11;?' in block:
                    os.write(self.master, b'\x1b]11;rgb:ffff/ffff/ffff\x1b\\')
        raise AssertionError('终端未显示：' + text)

    def close(self, already_exited=False):
        if not already_exited:
            self.send('q')
        self.process.wait(timeout=10)
        os.close(self.master)


try:
    # Intercept only official Release downloads; all other tools and URLs stay real.
    # This exercises production download/verify/install logic without publishing a test release.
    tools = fixture / 'tools'
    tools.mkdir()
    curl = tools / 'curl'
    curl.write_text('''#!/usr/bin/env python3
import os,sys,pathlib,shutil
args=sys.argv[1:];url=args[-1];target=args[args.index('--output')+1]
prefix='https://github.com/MaoLikeQvQ/MiProxy-Panel/releases/'
if not url.startswith(prefix):raise SystemExit('unexpected URL')
version=os.environ['TEST_RELEASE_VERSION']
relative=url[len(prefix):]
if relative=='latest/download/version.txt':name='version.txt'
elif relative.startswith('download/'+version+'/'):name=relative.split('/')[-1]
else:raise SystemExit('unexpected release URL')
source=pathlib.Path(os.environ['TEST_RELEASE_DIR'])/name
shutil.copyfile(source,target)
if ((os.environ.get('TEST_CORRUPT')=='binary' and name.startswith('miproxy-panel_'))
    or (os.environ.get('TEST_CORRUPT')=='archive' and name=='deploy.tar.gz')):
 with open(target,'ab') as output:output.write(b'corrupt')
''')
    curl.chmod(0o755)
    installer_env = env | {'PATH': str(tools) + ':' + env['PATH'],
                           'TEST_RELEASE_DIR': str(assets), 'TEST_RELEASE_VERSION': release_version}
    for corrupt in ['binary', 'archive']:
        bad = run('sh', str(root / 'install.sh'), env=installer_env | {'TEST_CORRUPT': corrupt}, check=False)
        assert bad.returncode != 0 and 'SHA-256' in bad.stdout, bad.stdout
        assert not Path('/usr/local/bin/miproxy-panel').exists()
    for arguments in [('--arch', 'unsupported'), ('--version', 'invalid')]:
        bad = run('sh', str(root / 'install.sh'), *arguments, env=installer_env, check=False)
        assert bad.returncode != 0, bad.stdout
        assert not Path('/usr/local/bin/miproxy-panel').exists()
    print('PASS: 二进制/部署包损坏、无效版本和架构均拒绝安装，未修改目标路径')

    result = run('sh', str(root / 'install.sh'), env=installer_env)
    print(result.stdout)
    assert run('miproxy', '--version').stdout.strip() == 'MiProxy Panel ' + release_version
    assert run('systemctl', 'is-active', 'miproxy-panel.service').stdout.strip() == 'active'
    assert run('systemctl', 'is-enabled', 'miproxy-panel.service').stdout.strip() == 'enabled'
    wait_api()
    with urllib.request.urlopen('http://127.0.0.1:8787/') as response:
        assert b'<html' in response.read().lower()
    assert Path('/etc/miproxy-panel.env').stat().st_mode & 0o777 == 0o600
    assert Path('/etc/mihomo/.miproxy/panel.sock').stat().st_mode & 0o777 == 0o600
    print('PASS: 从 Release 安装、miproxy 命令、真实 systemd、自启动、Web 与本机 Socket')

    terminal = Terminal()
    terminal.wait('运行概览')
    terminal.send('3')
    terminal.wait('导入订阅')
    yaml = fixture / 'subscription.yaml'
    yaml.write_text('proxies: [{name: local, type: direct}]\nrules: ["MATCH,DIRECT"]\n')
    terminal.send('f'); terminal.wait('订阅名称')
    terminal.send('Linux验收\r'); terminal.wait('YAML 文件的绝对路径')
    terminal.send(str(yaml) + '\r'); terminal.wait('本地订阅已导入')
    terminal.wait('共 1 个订阅')
    terminal.send('d'); terminal.wait('确认操作')
    terminal.send('y'); terminal.wait('订阅已移除')
    terminal.send('2'); terminal.wait('停止面板服务')
    terminal.send('\r'); terminal.wait('确认操作')
    terminal.send('y'); terminal.wait('停止完成')
    terminal.wait('启动面板服务'); wait_api(False)
    terminal.send('\r'); terminal.wait('确认操作')
    terminal.send('y'); terminal.wait('启动完成'); wait_api()
    terminal.wait('停止面板服务')
    terminal.send('\x1b[B\x1b[B\r'); terminal.wait('确认操作')
    terminal.send('y'); terminal.wait('关闭开机启动完成')
    terminal.wait('开启开机启动')
    terminal.send('\r'); terminal.wait('确认操作')
    terminal.send('y'); terminal.wait('开启开机启动完成')
    terminal.close()
    wait_api()
    print('PASS: 真实终端订阅导入删除、服务停止/启动、自启动关闭/开启、退出后服务继续运行')

    config = Path('/etc/mihomo/config.yaml')
    config.write_text('mixed-port: 17890\nrules: ["MATCH,DIRECT"]\n')
    env_path = Path('/etc/miproxy-panel.env')
    env_path.write_text(env_path.read_text() + '# acceptance: preserve existing configuration\n')
    before = hashlib.sha256(env_path.read_bytes()).digest()
    run('sh', str(root / 'install.sh'), '--version', release_version, env=installer_env)
    assert hashlib.sha256(env_path.read_bytes()).digest() == before
    assert '17890' in config.read_text()
    wait_api()
    print('PASS: 指定版本重复安装保留环境文件与配置')

    terminal = Terminal(); terminal.wait('运行概览')
    terminal.send('7'); terminal.wait('卸载面板与命令')
    terminal.send('\x1b[B\x1b[B\r'); terminal.wait('确认操作')
    terminal.send('y'); terminal.close(already_exited=True)
    assert not Path('/usr/local/bin/miproxy-panel').exists()
    assert not Path('/usr/local/bin/miproxy').exists()
    assert env_path.exists() and config.exists()
    wait_api(False)
    print('PASS: 真实终端卸载移除命令和服务，保留配置')

    run('sh', str(root / 'install.sh'), '--no-start', env=installer_env)
    assert run('systemctl', 'is-active', 'miproxy-panel.service', check=False).returncode != 0
    assert run('systemctl', 'is-enabled', 'miproxy-panel.service').stdout.strip() == 'enabled'
    terminal = Terminal(); terminal.wait('运行概览')
    terminal.send('2'); terminal.wait('启动面板服务')
    terminal.send('\r'); terminal.wait('确认操作')
    terminal.send('y'); terminal.wait('启动完成'); terminal.close()
    wait_api()
    print('PASS: --no-start 安装与离线终端启动服务')
finally:
    shutil.rmtree(fixture)
