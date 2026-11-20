"""Export one stopped managed VM independent of its original backing image."""

import hashlib
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

action, installation, project, scope = sys.argv[1:]
if not re.fullmatch(r"[a-f0-9]{32}", installation) or not all(
    re.fullmatch(r"[A-Za-z0-9_.-]{1,80}", item) for item in (project, scope)
):
    sys.exit("Invalid managed VM identity")
cache = Path("/tmp") / ("zentrale-microvm-" + str(os.getuid()))
if scope.startswith("project-"):
    kind, identity = "projects", installation + "\0" + project
else:
    kind, identity = "agents", project + "\0" + scope
directory = cache / kind / hashlib.sha256(identity.encode()).hexdigest()
disk = directory / "root.qcow2"
if disk.resolve() != disk or (disk.exists() and not disk.is_file()):
    sys.exit("Unsafe managed VM disk")
if action == "check":
    print("present" if disk.is_file() else "missing")
elif action == "retire":
    if directory.exists():
        if any(os.path.lexists(directory / entry) for entry in ("identity.json", "session", "root.qcow2")):
            sys.exit("The old VM has not been removed")
        shutil.rmtree(directory)
elif action == "export":
    if not disk.is_file():
        sys.exit("The managed VM disk disappeared before export")
    # Flatten the backing chain while the VM is stopped. The result remains
    # usable even if the old host's Fedora base image is later removed.
    with tempfile.TemporaryDirectory(prefix="move-", dir=directory) as staging:
        image = Path(staging) / "root.qcow2"
        subprocess.run(["qemu-img", "convert", "-c", "-O", "qcow2", str(disk), str(image)], check=True)
        subprocess.run(["qemu-img", "check", str(image)], check=True, stdout=sys.stderr)
        with image.open("rb") as source:
            shutil.copyfileobj(source, sys.stdout.buffer, 1024 * 1024)
else:
    sys.exit("Invalid VM snapshot operation")
