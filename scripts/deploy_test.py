#!/usr/bin/env python3
"""Test deployment guards in a temporary tree with mocked build/service commands."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class DeployTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="mineagent-deploy-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "scripts").mkdir()
        shutil.copyfile(Path(__file__).with_name("deploy.sh"), self.root / "scripts/deploy.sh")
        (self.root / "bin").mkdir()
        self.binary = self.root / "bin/mineagent"
        self.binary.write_text("old-version")
        (self.root / "data").mkdir()
        self.token = self.root / "data/deploy-token.txt"
        self.token.write_text("test-token")
        self.config = self.root / "config.json"
        self.config.write_text(json.dumps({"web": {"listen": ":8766", "dataDir": str(self.root / "data")}}))
        self.trace = self.root / "trace"
        mocks = self.root / "mocks"
        mocks.mkdir()
        commands = {
            "make": """
log('build')
if os.environ.get('MOCK_BUILD_FAIL'): sys.exit(2)
output = next(a.split('=', 1)[1] for a in sys.argv if a.startswith('BUILD_OUTPUT='))
Path(output).write_text('new-version')
""",
            "curl": """
if sys.argv[-1].endswith('/api/games/active'):
    log('query')
    assert '-fsS' in sys.argv
    header_arg = sys.argv[sys.argv.index('-H') + 1]
    assert header_arg.startswith('@'), 'token exposed in command arguments'
    assert Path(header_arg[1:]).read_text() == 'Authorization: Bearer test-token\\n'
    if os.environ.get('MOCK_CURL_EXIT'): sys.exit(int(os.environ['MOCK_CURL_EXIT']))
    print(os.environ.get('MOCK_RESPONSE', '{"playing":0}'))
else:
    print('200')
""",
            "sudo": "log('restart')\nassert sys.argv[1:] == ['systemctl', 'restart', 'mineagent']\n",
            "systemctl": "log('status')\nprint('active')\n",
            "sleep": "",
        }
        for name, body in commands.items():
            path = mocks / name
            path.write_text("#!/usr/bin/env python3\nimport os, sys\nfrom pathlib import Path\n"
                            "def log(text):\n    with open(os.environ['MOCK_TRACE'], 'a') as f: f.write(text + '\\n')\n" + body)
            path.chmod(0o755)
        self.env = dict(os.environ, PATH=str(mocks) + ":" + os.environ["PATH"], MOCK_TRACE=str(self.trace), CONFIG=str(self.config))
        self.env.pop("TOKEN_FILE", None)

    def run_deploy(self, *args, **env):
        return subprocess.run(["bash", str(self.root / "scripts/deploy.sh"), *args],
                              env=dict(self.env, **env), capture_output=True, text=True, timeout=10)

    def assert_blocked(self, result):
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.binary.read_text(), "old-version")
        self.assertNotIn("restart", self.trace.read_text() if self.trace.exists() else "")

    def test_invalid_responses_cannot_replace_binary_or_restart(self):
        for response in ['{"error":"needs admin"}', '{}', '[]', 'not-json',
                         '{"playing":false}', '{"playing":"0"}', '{"playing":-1}', '{"playing":1}']:
            with self.subTest(response=response):
                self.assert_blocked(self.run_deploy(MOCK_RESPONSE=response))

    def test_http_and_network_failures_block(self):
        for code in [7, 22, 28]:
            with self.subTest(code=code):
                self.assert_blocked(self.run_deploy(MOCK_CURL_EXIT=str(code)))

    def test_missing_or_empty_token_blocks(self):
        self.token.unlink()
        self.assert_blocked(self.run_deploy())
        self.token.write_text("")
        self.assert_blocked(self.run_deploy())

    def test_invalid_config_blocks(self):
        self.config.write_text("not-json")
        self.assert_blocked(self.run_deploy())

    def test_build_failure_blocks(self):
        self.assert_blocked(self.run_deploy(MOCK_BUILD_FAIL="1"))

    def test_success_installs_after_check(self):
        result = self.run_deploy()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.binary.read_text(), "new-version")
        self.assertEqual(self.trace.read_text().splitlines(), ["build", "query", "restart", "status"])

    def test_force_explicitly_bypasses_check(self):
        self.token.unlink()
        result = self.run_deploy("--force")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.binary.read_text(), "new-version")
        self.assertNotIn("query", self.trace.read_text())

    def test_unknown_argument_blocks(self):
        self.assert_blocked(self.run_deploy("--froce"))


if __name__ == "__main__":
    unittest.main()
