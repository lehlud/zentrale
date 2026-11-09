"""Prepare a project directory inside configured persistent storage over SSH."""

import json
import os
import sys
import tempfile
from pathlib import Path


def prepare(configuration):
    project_name = "project-" + configuration["projectId"]
    pinned = configuration.get("directory")
    root = (
        Path(pinned).parent
        if pinned
        else Path(os.path.expanduser(configuration["root"]))
    )
    if not root.is_absolute() or str(root.resolve()) == "/":
        raise ValueError("workspace root must be a persistent directory other than /")
    root = root.resolve()
    directory = root / project_name
    metadata = root / ".zentrale-workspaces"
    marker = metadata / (project_name + ".json")

    if metadata.is_symlink() or marker.is_symlink() or directory.is_symlink():
        raise ValueError("managed workspace metadata and project directories must not be symlinks")
    if configuration["initialized"]:
        if not marker.is_file() or not directory.is_dir():
            raise ValueError(
                "project workspace is missing on this machine; "
                "pool members must mount the same persistent storage"
            )
    else:
        root.mkdir(mode=0o700, parents=True, exist_ok=True)
        metadata.mkdir(mode=0o700, exist_ok=True)
        if not marker.exists():
            if directory.exists() and any(directory.iterdir()):
                raise ValueError("refusing to adopt an existing non-empty project directory")
            directory.mkdir(mode=0o700, exist_ok=True)
            # Publish a complete receipt atomically. A canceled SSH command
            # must never leave a half-written marker that prevents recovery.
            with tempfile.NamedTemporaryFile(mode="w", dir=metadata, delete=False) as output:
                temporary = Path(output.name)
                try:
                    json.dump({"token": configuration["token"]}, output)
                    output.flush()
                    os.fsync(output.fileno())
                    try:
                        os.link(temporary, marker)
                    except FileExistsError:
                        # Another machine alias may reach the same shared root.
                        # Validate its complete receipt below instead of replacing it.
                        pass
                finally:
                    temporary.unlink(missing_ok=True)

    with marker.open() as source:
        receipt = json.load(source)
    if receipt.get("token") != configuration["token"]:
        raise ValueError("project workspace belongs to a different installation or project")
    if not directory.is_dir() or not os.access(directory, os.R_OK | os.W_OK | os.X_OK):
        raise ValueError("project workspace is not readable and writable")
    return {"directory": str(directory)}


if __name__ == "__main__":
    try:
        print(json.dumps(prepare(json.load(sys.stdin))))
    except (OSError, ValueError) as error:
        sys.exit("Project workspace: " + str(error))
