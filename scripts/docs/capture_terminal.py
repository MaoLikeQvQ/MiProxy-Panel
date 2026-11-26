"""Capture the actual TUI in a PTY against an isolated demo instance."""
import argparse, fcntl, json, os, pty, select, signal, socket, struct, subprocess, tempfile, termios, time, urllib.request
from pathlib import Path
parser=argparse.ArgumentParser()
parser.add_argument('--binary',required=True)
parser.add_argument('--output',required=True)
args=parser.parse_args()
binary=str(Path(args.binary).resolve())
with tempfile.TemporaryDirectory(prefix='mp-doc-',dir='/tmp') as folder:
    config=Path(folder)/'config.yaml'
    config.write_text('rules: ["MATCH,DIRECT"]\n')
    with socket.socket() as port:
        port.bind(('127.0.0.1',0));address='127.0.0.1:'+str(port.getsockname()[1])
    env={k:v for k,v in os.environ.items() if not k.startswith(('PANEL_','MIHOMO_'))}
    env.update(PANEL_CONFIG=str(config),PANEL_LISTEN=address,PANEL_SOCKET=folder+'/panel.sock',TERM='xterm-256color',COLORTERM='truecolor')
    env.pop('NO_COLOR',None)
    env.pop('MIPROXY_ENV_FILE',None)
    env['MIHOMO_CONTROLLER']='http://127.0.0.1:1'
    panel=subprocess.Popen([binary,'serve'],env=env,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    terminal=None
    try:
        for attempt in range(50):
            try: urllib.request.urlopen('http://'+address+'/api/config',timeout=1).close();break
            except OSError: time.sleep(.1)
        for name in ['日常使用','备用线路']:
            data=json.dumps({'name':name,'content':'proxies: [{name: Demo, type: direct}]\nrules: ["MATCH,DIRECT"]\n'}).encode()
            request=urllib.request.Request('http://'+address+'/api/subscriptions',data=data,headers={'Content-Type':'application/json'})
            urllib.request.urlopen(request,timeout=5).close()
        master,slave=pty.openpty()
        fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',30,112,0,0))
        terminal=subprocess.Popen([binary,'tui'],stdin=slave,stdout=slave,stderr=slave,env=env,start_new_session=True)
        os.close(slave)
        captured=bytearray();end=time.monotonic()+3;sent=False
        while time.monotonic()<end:
            if select.select([master],[],[],.1)[0]:
                data=os.read(master,65536);captured.extend(data)
                if b'\x1b]11;?' in data: os.write(master,b'\x1b]11;rgb:1717/2222/3434\x1b\\')
            if not sent and time.monotonic()>end-1.7: os.write(master,b'3');sent=True
        Path(args.output).write_bytes(captured)
        os.write(master,b'q');terminal.wait(timeout=5);os.close(master)
    finally:
        if terminal and terminal.poll() is None: os.killpg(terminal.pid,signal.SIGTERM);terminal.wait(timeout=5)
        panel.terminate();panel.wait(timeout=10)
