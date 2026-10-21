#!/usr/bin/env python3
"""Isolated installed-DSH integration: local fake model, no live profile or API key.
Requires dsh on PATH and its installed package directory (DSH_TEST_MODULE_ROOT).
"""
import http.server
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request


def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


class Model(http.server.BaseHTTPRequestHandler):
    calls = []

    def log_message(self, *args):
        pass

    def do_POST(self):
        payload = self.rfile.read(int(self.headers['Content-Length']))
        if not self.headers.get('Content-Type', '').startswith('application/json'):
            self.send_response(400)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(b'{"error":{"type":"invalid_request_error","message":"fixture uses inline images"}}')
            return
        data = json.loads(payload)
        self.calls.append(data)
        messages = data.get('messages', [])
        text = json.dumps(messages)
        result = any('tool_result' in json.dumps(m.get('content')) for m in messages)
        use_tool = ('TOOLTEST' in text or 'CANCELTEST' in text or 'BUDGETTEST' in text or 'NATIVETEST' in text) and (not result or 'BUDGETTEST' in text)
        tool = 'bash' if 'NATIVETEST' in text else 'fixture_tool'
        content = [{'type': 'tool_use', 'id': 'call-' + str(len(self.calls)), 'name': tool, 'input': {'value': 'fixture'}}] if use_tool else [{'type': 'text', 'text': 'OK'}]
        events = [('message_start', {'type': 'message_start', 'message': {'id': 'fixture', 'type': 'message', 'role': 'assistant', 'model': data['model'], 'content': [], 'stop_reason': None, 'stop_sequence': None, 'usage': {'input_tokens': 20, 'output_tokens': 0}}})]
        for i, block in enumerate(content):
            if block['type'] == 'tool_use':
                start = {**block, 'input': {}}
                delta = {'type': 'input_json_delta', 'partial_json': json.dumps(block['input'])}
            else:
                start = {'type': 'text', 'text': ''}
                delta = {'type': 'text_delta', 'text': block['text']}
            events += [('content_block_start', {'type': 'content_block_start', 'index': i, 'content_block': start}), ('content_block_delta', {'type': 'content_block_delta', 'index': i, 'delta': delta}), ('content_block_stop', {'type': 'content_block_stop', 'index': i})]
        events += [('message_delta', {'type': 'message_delta', 'delta': {'stop_reason': 'tool_use' if use_tool else 'end_turn', 'stop_sequence': None}, 'usage': {'output_tokens': 5}}), ('message_stop', {'type': 'message_stop'})]
        payload = ''.join('event: ' + name + '\ndata: ' + json.dumps(value) + '\n\n' for name, value in events).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.send_header('Content-Length', str(len(payload)))
        self.end_headers()
        try:
            self.wfile.write(payload)
        except (BrokenPipeError, ConnectionResetError):
            pass


def main():
    repo = Path(__file__).resolve().parents[1]
    dsh = shutil.which('dsh')
    if not dsh:
        raise SystemExit('dsh is required')
    modules = Path(os.environ.get('DSH_TEST_MODULE_ROOT', str(Path(dsh).resolve().parents[1] / 'node_modules')))
    if not (modules / '@deepseek-ai/dsh-llm').is_dir():
        raise SystemExit('set DSH_TEST_MODULE_ROOT to the installed DSH node_modules')
    with tempfile.TemporaryDirectory(prefix='mineagent-dsh-e2e-') as folder:
        root = Path(folder)
        root.chmod(0o700)
        plugin = root / 'plugin'
        shutil.copytree(repo / 'dsh-bridge', plugin, ignore=shutil.ignore_patterns('node_modules'))
        (plugin / 'node_modules').symlink_to(modules, target_is_directory=True)
        # Diagnostics are injected only into this isolated copy with fake credentials.
        bridge_source = plugin / 'server.mjs'
        bridge_source.write_text(bridge_source.read_text().replace('} catch { /* No raw tool/model errors, tokens, arguments or URLs in transport logs. */ }', '} catch (error) { console.error("test-only bridge error", error); }').replace("if(payload.agent===agent)failed=true;", "if(payload.agent===agent){failed=true;console.error('test-only agent error',payload.error);}"))
        token = root / 'bridge.token'
        token.write_text(os.urandom(32).hex())
        token.chmod(0o600)
        model = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Model)
        threading.Thread(target=model.serve_forever, daemon=True).start()
        bridge_port, web_port = port(), port()
        patch = root / 'patch.yml'
        patch.write_text('- id: workspace-controller\n  config:\n    documentsDirectory: ' + json.dumps(str(root)) + '\n- insert:\n    - id: mineagent-dsh-bridge\n      name: ' + json.dumps(str(plugin / 'index.mjs')) + '\n      config:\n        host: 127.0.0.1\n        port: ' + str(bridge_port) + '\n        tokenFile: ' + json.dumps(str(token)) + '\n        cwd: ' + json.dumps(str(root)) + '\n')
        env = {k: v for k, v in os.environ.items() if k in ['PATH', 'HOME', 'LANG', 'LC_ALL', 'GOCACHE', 'GOTMPDIR']}
        env.update(DSH_HOME=str(root / 'home'), DSH_AGENTS_HOME=str(root / 'agents'), DEEPSEEK_API_KEY='local-fixture-only', DEEPSEEK_BASE_URL='http://127.0.0.1:' + str(model.server_port), NO_COLOR='1')
        with open(root / 'host.log', 'w+') as log:
            process = subprocess.Popen([dsh, '--profile', 'web', '--patch', str(patch), '--no-open', '--host', '127.0.0.1', '--port', str(web_port)], cwd=root, env=env, stdout=log, stderr=log, start_new_session=True)
            try:
                url = 'http://127.0.0.1:' + str(bridge_port)
                for _ in range(300):
                    if process.poll() is not None:
                        raise RuntimeError('isolated DSH exited')
                    try:
                        req = urllib.request.Request(url + '/v1/health', headers={'Authorization': 'Bearer ' + token.read_text()})
                        with urllib.request.urlopen(req, timeout=1) as response:
                            assert json.load(response)['protocol'] == 1
                        break
                    except (OSError, urllib.error.URLError):
                        time.sleep(.1)
                else:
                    raise RuntimeError('isolated bridge did not start')
                test_env = {**os.environ, 'MINEAGENT_TEST_DSH_URL': url, 'MINEAGENT_TEST_DSH_TOKEN_FILE': str(token)}
                subprocess.run(['go', 'test', './internal/dshbridge', '-run', 'TestInstalledDSH', '-count=1', '-v', '-timeout=90s'], cwd=repo, env=test_env, check=True)
                assert Model.calls, 'model was not invoked'
                assert all(c.get('thinking', {}).get('type') == 'enabled' and c.get('output_config', {}).get('effort') == 'high' for c in Model.calls), 'high reasoning was not enabled'
                assert any(b.get('type') == 'image' for c in Model.calls for m in c.get('messages', []) if isinstance(m.get('content'), list) for b in m['content']), 'image bytes did not reach model'
                first = Model.calls[0]['messages']
                assert [m['role'] for m in first] == ['user', 'assistant', 'user', 'assistant', 'user'], 'history roles changed'
                assert 'Earlier assistant reply' in json.dumps(first[1]), 'history was lost'
                assert all(set(t['name'] for t in c.get('tools', [])) <= {'fixture_tool'} for c in Model.calls), 'native tools leaked'
                print('Installed DSH Web bridge: reasoning, history, image, scoped tools, cancellation and budget passed.')
            except Exception:
                # This fresh test profile only contains fake credentials. Print
                # selected diagnostics; never print Web launch URLs or tokens.
                log.flush()
                for line in (root / 'host.log').read_text().splitlines():
                    if ('error' in line.lower() or 'failed' in line.lower()) and 'http' not in line and token.read_text() not in line:
                        print(line[:600])
                raise
            finally:
                if process.poll() is None:
                    os.killpg(process.pid, signal.SIGTERM)
                    try:
                        process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        os.killpg(process.pid, signal.SIGKILL)
                        process.wait()
                model.shutdown()


if __name__ == '__main__':
    main()
