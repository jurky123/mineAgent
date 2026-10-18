#!/usr/bin/env python3
"""Create a private deployable Worker without printing its signing credential."""
import argparse
import json
import os
import pathlib
import re
import tempfile

p = argparse.ArgumentParser()
p.add_argument('--config', type=pathlib.Path, required=True)
p.add_argument('--output', type=pathlib.Path, required=True)
a = p.parse_args()
os.umask(0o077)
project = pathlib.Path(__file__).resolve().parents[1]
output = a.output.resolve()
if output == project or project in output.parents:
    raise SystemExit('Private Worker output must be outside the source repository.')
config = json.loads(a.config.read_text())
secret = config.get('web', {}).get('downloadProxySecret', '')
if not re.fullmatch('[a-f0-9]{64}', secret):
    raise SystemExit('Configure a valid private downloadProxySecret first.')
source = (project/'scripts/cloudflare-download-worker.mjs').read_text()
placeholder = "const PRIVATE_SIGNING_SECRET = '';"
if source.count(placeholder) != 1:
    raise SystemExit('Worker template does not have a unique private configuration placeholder.')
source = source.replace(placeholder, 'const PRIVATE_SIGNING_SECRET = '+json.dumps(secret)+';')
output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
if output.parent.stat().st_mode & 0o077:
    raise SystemExit('The output directory must be private (0700).')
fd, temporary = tempfile.mkstemp(prefix='.worker-', dir=output.parent)
try:
    with os.fdopen(fd, 'w') as f:
        f.write(source)
        f.flush()
        os.fsync(f.fileno())
    os.replace(temporary, output)
finally:
    pathlib.Path(temporary).unlink(missing_ok=True)
print('Private Worker created:', output)
print('After deployment, GET /api/downloads/status must report signedIP=true.')
