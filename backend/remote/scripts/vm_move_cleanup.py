"""Clean up a removed managed VM after moving its project."""

import hashlib
import os
import re
import shutil
import sys
from pathlib import Path

installation, project, scope = sys.argv[1:]
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
if directory.resolve() != directory:
    sys.exit("Unsafe managed VM directory")
if directory.exists():
    if any(os.path.lexists(directory / entry) for entry in ("identity.json", "session", "root.qcow2")):
        sys.exit("The old VM has not been removed")
    shutil.rmtree(directory)
