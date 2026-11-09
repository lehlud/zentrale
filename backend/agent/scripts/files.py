"""Fixed remote file operations; never interpret caller input as Python code."""

import base64
import difflib
import hashlib
import json
import os
import shutil
import sys
import tempfile

MAX_DIFF_INPUT = 256 * 1024
MAX_DIFF_LINES = 5000
MAX_PATCH_BYTES = 128 * 1024
MAX_PATCH_LINES = 800


def text_snapshot(content, exists=True):
    if len(content) > MAX_DIFF_INPUT:
        return {"exists": exists, "text": None, "notice": "too_large"}

    try:
        text = content.decode("utf-8")
    except UnicodeDecodeError:
        return {"exists": exists, "text": None, "notice": "binary"}
    if "\x00" in text:
        return {"exists": exists, "text": None, "notice": "binary"}
    if len(text.splitlines()) > MAX_DIFF_LINES:
        return {"exists": exists, "text": None, "notice": "too_large"}
    return {"exists": exists, "text": text, "notice": ""}


def snapshot_file(filename):
    if os.path.isdir(filename):
        return {"exists": True, "text": None, "notice": "directory"}
    try:
        with open(filename, "rb") as source:
            return text_snapshot(source.read(MAX_DIFF_INPUT + 1))
    except FileNotFoundError:
        return text_snapshot(b"", exists=False)
    except OSError:
        # Optional presentation must not prevent an otherwise permitted write.
        return {"exists": True, "text": None, "notice": "unavailable"}


def file_change(filename, before, after, workspace):
    kind = "modified" if before["exists"] else "created"
    if not after["exists"]:
        kind = "deleted"
    change = {
        "path": filename,
        "kind": kind,
        "patch": "",
        "added": 0,
        "removed": 0,
        "truncated": False,
        "notice": before["notice"] or after["notice"],
    }
    if change["notice"]:
        return change
    if before["exists"] and after["exists"] and before["text"] == after["text"]:
        change["kind"] = "unchanged"
        return change

    relative = os.path.relpath(filename, workspace)
    old_name = "a/" + relative if before["exists"] else "/dev/null"
    new_name = "b/" + relative if after["exists"] else "/dev/null"
    patch, size, in_hunk = [], 0, False
    for line in difflib.unified_diff(
        before["text"].splitlines(keepends=True),
        after["text"].splitlines(keepends=True),
        fromfile=old_name,
        tofile=new_name,
    ):
        if line.startswith("@@ "):
            in_hunk = True
        if in_hunk:
            change["added"] += int(line.startswith("+"))
            change["removed"] += int(line.startswith("-"))
        if not line.endswith("\n"):
            line += "\n\\ No newline at end of file\n"
        size += len(line.encode("utf-8"))
        if size <= MAX_PATCH_BYTES and len(patch) < MAX_PATCH_LINES:
            patch.append(line)
        else:
            change["truncated"] = True
    change["patch"] = "".join(patch)
    return change


def within(directory, filename):
    return filename == directory or filename.startswith(directory + os.sep)


def read_file(filename, options=None):
    with open(filename, "rb") as source:
        content = source.read(2 * 1024 * 1024 + 1)
    if len(content) > 2 * 1024 * 1024:
        raise ValueError("file exceeds 2 MiB")
    result = {
        "path": filename,
        "content": content.decode("utf-8", errors="replace"),
        "sha256": hashlib.sha256(content).hexdigest(),
    }

    if options:
        offset, limit = options["offset"], options["limit"]
        if (
            not isinstance(offset, int)
            or not isinstance(limit, int)
            or offset < 1
            or not 1 <= limit <= 2000
        ):
            raise ValueError("invalid file line range")
        lines = result["content"].splitlines(keepends=True)
        result.update(
            content="".join(lines[offset - 1 : offset - 1 + limit]),
            offset=offset,
            totalLines=len(lines),
            hasMore=offset - 1 + limit < len(lines),
        )
    return result


def stage_file(filename, content, mode=None):
    """Prepare a same-filesystem replacement without changing the destination."""
    os.makedirs(os.path.dirname(filename), exist_ok=True)
    descriptor, temporary_file = tempfile.mkstemp(dir=os.path.dirname(filename))
    try:
        with os.fdopen(descriptor, "wb") as target:
            target.write(content)
        if mode is not None:
            os.chmod(temporary_file, mode)
        return temporary_file
    except BaseException:
        if os.path.exists(temporary_file):
            os.unlink(temporary_file)
        raise


def write_file(filename, encoded_content, workspace):
    content = base64.b64decode(encoded_content)
    before = snapshot_file(filename)
    mode = os.stat(filename).st_mode if os.path.isfile(filename) else None
    temporary_file = stage_file(filename, content, mode)
    try:
        os.replace(temporary_file, filename)
    finally:
        if os.path.exists(temporary_file):
            os.unlink(temporary_file)
    return {
        "path": filename,
        "written": True,
        "_change": file_change(filename, before, text_snapshot(content), workspace),
    }


def main():
    workspace, temporary_directory, requested_path, action, content, *options = (
        sys.argv[1:]
    )
    if action == "write_file" and content == "-":
        content = sys.stdin.read()
    workspace = os.path.realpath(workspace)
    temporary_directory = os.path.realpath(temporary_directory)
    filename = os.path.realpath(
        requested_path
        if os.path.isabs(requested_path)
        else os.path.join(workspace, requested_path)
    )
    if not any(within(root, filename) for root in (workspace, temporary_directory)):
        raise ValueError("path leaves the project and its managed temporary directory")
    if action in ("write_file", "delete_file") and filename in (
        workspace,
        temporary_directory,
    ):
        raise ValueError("cannot overwrite or remove a workspace root")

    if action == "read_file":
        result = read_file(filename, json.loads(options[0]) if options else None)
    elif action == "write_file":
        result = write_file(filename, content, workspace)
    elif action == "list_files":
        entries = sorted(os.scandir(filename), key=lambda entry: entry.name)
        result = {
            "path": filename,
            "entries": [
                {
                    "name": entry.name,
                    "directory": entry.is_dir(follow_symlinks=False),
                    "symlink": entry.is_symlink(),
                }
                for entry in entries[:500]
            ],
        }
    elif action == "delete_file":
        before = snapshot_file(filename)
        if os.path.isdir(filename):
            shutil.rmtree(filename)
        else:
            os.unlink(filename)
        result = {
            "path": filename,
            "deleted": True,
            "_change": file_change(
                filename, before, text_snapshot(b"", exists=False), workspace
            ),
        }
    else:
        raise ValueError("unknown file operation")

    print(json.dumps(result))


if __name__ == "__main__":
    main()
