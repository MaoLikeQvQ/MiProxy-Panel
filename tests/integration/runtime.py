#!/usr/bin/env python3
"""Exercise a compiled panel's real HTTP auth and private socket in a temp directory."""
import argparse
import base64
import http.client
import json
import os
import pty
import select
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument('--binary', required=True)
args = parser.parse_args()
binary = str(Path(args.binary).resolve())
with tempfile.TemporaryDirectory(prefix='mp-smoke-', dir='/tmp') as folder:
    config = Path(folder) / 'config.yaml'
    config.write_text('rules: ["MATCH,DIRECT"]\n')
    with socket.socket() as reservation:
        reservation.bind(('127.0.0.1', 0))
        address = '127.0.0.1:' + str(reservation.getsockname()[1])
    socket_path = folder + '/panel.sock'
    env = {key: value for key, value in os.environ.items() if not key.startswith(('PANEL_', 'MIHOMO_'))}
    env.update(PANEL_CONFIG=str(config), PANEL_LISTEN=address, PANEL_SOCKET=socket_path, MIHOMO_CONTROLLER='http://127.0.0.1:1', PANEL_PASSWORD='obsolete-ignored', MIHOMO_SECRET='obsolete-ignored')
    process = subprocess.Popen([binary, 'serve'], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    def request(path, body=None, password=None):
        headers = {}
        if body is not None:
            headers['Content-Type'] = 'application/json'
        if password:
            headers['Authorization'] = 'Basic ' + base64.b64encode(('admin:' + password).encode()).decode()
        data = json.dumps(body).encode() if body is not None else None
        call = urllib.request.Request('http://' + address + path, data=data, headers=headers)
        try:
            with urllib.request.urlopen(call, timeout=5) as response:
                return response.status, response.read()
        except urllib.error.HTTPError as error:
            return error.code, error.read()

    try:
        for attempt in range(50):
            try:
                status, _ = request('/api/config')
                if status == 200:
                    break
            except OSError:
                time.sleep(.1)
        assert status == 200, 'Removed password env must not configure auth'
        assert request('/')[0] == 200, 'Embedded Web must load from new package'
        status, body = request('/api/settings/access', {'password': 'runtime-example-password'})
        assert status == 200 and json.loads(body)['passwordSet']
        assert request('/api/config')[0] == 401
        assert request('/api/config', password='wrong')[0] == 401
        assert request('/api/config', password='runtime-example-password')[0] == 200
        stored = Path(str(config) + '.access.json')
        assert 'runtime-example-password' not in stored.read_text()
        assert stored.stat().st_mode & 0o777 == 0o600
        connection = http.client.HTTPConnection('localhost')
        connection.sock = socket.socket(socket.AF_UNIX)
        connection.sock.connect(socket_path)
        connection.request('POST', '/api/settings/access', body=json.dumps({'password': ''}), headers={'Content-Type': 'application/json'})
        response = connection.getresponse()
        assert response.status == 200, response.read()
        response.read()
        connection.close()
        assert request('/api/config')[0] == 200
        master, slave = pty.openpty()
        terminal = subprocess.Popen([binary, 'access'], env=env, stdin=slave, stdout=slave, stderr=slave)
        os.close(slave)
        captured = bytearray()

        def wait_prompt(text):
            deadline = time.monotonic() + 5
            while text.encode() not in captured:
                if time.monotonic() > deadline:
                    raise AssertionError('Missing access prompt: ' + text)
                if select.select([master], [], [], .1)[0]:
                    captured.extend(os.read(master, 65536))

        try:
            wait_prompt('新密码')
            os.write(master, b'cli-example-password' + bytes([10]))
            wait_prompt('再次输入密码')
            os.write(master, b'cli-example-password' + bytes([10]))
            wait_prompt('访问密码已保存')
            terminal.wait(timeout=5)
            assert terminal.returncode == 0
            assert b'cli-example-password' not in captured, 'Password echoed in terminal'
            assert request('/api/config')[0] == 401
            assert request('/api/config', password='cli-example-password')[0] == 200
        finally:
            if terminal.poll() is None:
                terminal.terminate()
                terminal.wait(timeout=5)
            os.close(master)
        print('PASS compiled runtime: embedded Web, removed env ignored, persisted auth, Socket recovery, and hidden-input access CLI')
    finally:
        process.terminate()
        process.wait(timeout=10)
