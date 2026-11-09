"""Execute only the administrator-configured, fixed host quota query."""

import json
import os
import re
import subprocess
import sys

command = json.load(sys.stdin)["command"]
if (
    not isinstance(command, list)
    or not 1 <= len(command) <= 16
    or not all(
        isinstance(item, str) and item and "\x00" not in item for item in command
    )
    or not os.path.isabs(command[0])
):
    raise ValueError("invalid configured quota command")

environment = dict(
    os.environ, PATH="/usr/sbin:/usr/bin:/sbin:/bin", LC_ALL="C", LANGUAGE="C"
)
result = subprocess.run(
    command,
    capture_output=True,
    text=True,
    errors="replace",
    timeout=25,
    env=environment,
)

output = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", result.stdout + result.stderr)
print(
    json.dumps(
        {
            "output": output[:64000],
            "exitCode": result.returncode,
            "truncated": len(output) > 64000,
        }
    )
)
