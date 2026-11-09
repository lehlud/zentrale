"""Apply the Codex patch format inside the reviewed remote project.

Parse and validate the entire patch before touching files. Stage replacements
and backups before committing; restore earlier files if a later write fails.
No shell, subprocess or fuzzy content replacement is involved.
"""

import json
import os
import sys

from zentrale_files import file_change, stage_file, text_snapshot, within

MAX_PATCH_BYTES = 1024 * 1024
MAX_FILE_BYTES = 2 * 1024 * 1024
MAX_TOTAL_BYTES = 16 * 1024 * 1024
FILE_MARKERS = ("*** Add File: ", "*** Update File: ", "*** Delete File: ")


def resolve_path(workspace, temporary_directory, requested):
    if not requested or "\x00" in requested:
        raise ValueError("patch needs a nonempty file path")
    filename = os.path.realpath(os.path.join(workspace, requested))
    roots = (workspace, temporary_directory)
    if filename in roots or not any(within(root, filename) for root in roots):
        raise ValueError("patch path leaves the project or overwrites a workspace root")
    return filename


def parse_patch(patch):
    if len(patch.encode("utf-8")) > MAX_PATCH_BYTES or "\x00" in patch:
        raise ValueError("patch exceeds 1 MiB or contains a NUL character")
    lines = patch.strip().splitlines()
    if len(lines) < 3 or lines[0] != "*** Begin Patch" or lines[-1] != "*** End Patch":
        raise ValueError(
            "patch must start with *** Begin Patch and end with *** End Patch"
        )

    operations, index = [], 1
    while index < len(lines) - 1:
        header = lines[index]
        marker = next((item for item in FILE_MARKERS if header.startswith(item)), None)
        if marker is None:
            raise ValueError("expected Add File, Update File or Delete File marker")
        operation = {
            "kind": marker.split()[1].lower(),
            "path": header[len(marker) :],
            "lines": [],
        }
        index += 1
        if operation["kind"] == "update" and lines[index].startswith("*** Move to: "):
            operation["move"] = lines[index][len("*** Move to: ") :]
            index += 1
        while index < len(lines) - 1 and not lines[index].startswith(FILE_MARKERS):
            operation["lines"].append(lines[index])
            index += 1
        operations.append(operation)
        if len(operations) > 50:
            raise ValueError("patch may change at most 50 files")
    return operations


def update_chunks(lines):
    chunks, current = [], None
    for line in lines:
        if line == "@@" or line.startswith("@@ "):
            if current is not None:
                chunks.append(current)
            current = {
                "anchor": line[3:] if line.startswith("@@ ") else "",
                "old": [],
                "new": [],
                "eof": False,
            }
        elif line == "*** End of File":
            if current is None or current["eof"]:
                raise ValueError("unexpected End of File marker")
            current["eof"] = True
        elif line[:1] in (" ", "+", "-"):
            if current is None:
                current = {"anchor": "", "old": [], "new": [], "eof": False}
            if current["eof"]:
                raise ValueError("End of File must end the change chunk")
            if line[0] != "+":
                current["old"].append(line[1:])
            if line[0] != "-":
                current["new"].append(line[1:])
        else:
            raise ValueError("update lines must begin with a space, + or -")
    if current is not None:
        chunks.append(current)

    empty_chunk = any(not chunk["old"] and not chunk["new"] for chunk in chunks)
    if not chunks or empty_chunk:
        raise ValueError("Update File requires a nonempty change chunk")
    return chunks


def updated_content(original, patch_lines):
    if b"\x00" in original:
        raise ValueError("cannot update a binary file with a text patch")
    text = original.decode("utf-8")
    uses_crlf = "\r\n" in text and text.count("\n") == text.count("\r\n")
    newline = "\r\n" if uses_crlf else "\n"
    lines = text.splitlines()
    output, cursor = [], 0
    for chunk in update_chunks(patch_lines):
        search_start = cursor
        if chunk["anchor"]:
            try:
                search_start = lines.index(chunk["anchor"], cursor) + 1
            except ValueError as failure:
                raise ValueError(
                    "patch anchor was not found: " + chunk["anchor"][:160]
                ) from failure
        old = chunk["old"]
        candidates = range(search_start, len(lines) - len(old) + 1)
        if chunk["eof"] or not old:
            candidates = [len(lines) - len(old)]
        position = next(
            (
                offset
                for offset in candidates
                if offset >= search_start and lines[offset : offset + len(old)] == old
            ),
            None,
        )
        if position is None:
            raise ValueError(
                "patch context did not match; read the current file before retrying"
            )
        output.extend(lines[cursor:position])
        output.extend(chunk["new"])
        cursor = position + len(old)

    output.extend(lines[cursor:])
    result = newline.join(output)
    if output and (text.endswith(("\n", "\r")) or not original):
        result += newline
    return result.encode("utf-8")


def prepare_changes(workspace, temporary_directory, operations):
    originals = {}
    modes = {}
    replacements = {}
    total = 0

    def reserve(requested):
        nonlocal total
        filename = resolve_path(workspace, temporary_directory, requested)
        if filename in originals:
            raise ValueError(
                "use one file marker with multiple @@ chunks for each path"
            )
        content, mode = None, None
        if os.path.lexists(filename):
            if not os.path.isfile(filename):
                raise ValueError("patch targets must be regular files")
            with open(filename, "rb") as source:
                content = source.read(MAX_FILE_BYTES + 1)
            if len(content) > MAX_FILE_BYTES:
                raise ValueError("patch target exceeds 2 MiB")
            mode = os.stat(filename).st_mode
            total += len(content)
        originals[filename], modes[filename] = content, mode
        return filename

    for operation in operations:
        filename = reserve(operation["path"])
        old = originals[filename]
        kind, lines = operation["kind"], operation["lines"]
        if kind == "add":
            if old is not None or any(not line.startswith("+") for line in lines):
                raise ValueError("Add File requires a new path and + prefixed lines")
            content = "\n".join(line[1:] for line in lines)
            if lines:
                content += "\n"
            replacements[filename] = content.encode("utf-8")
        else:
            if old is None:
                raise ValueError("patch target does not exist: " + operation["path"])
            if kind == "delete":
                if lines:
                    raise ValueError("Delete File does not accept content lines")
                replacements[filename] = None
            else:
                replacement = updated_content(old, lines)
                if operation.get("move"):
                    destination = reserve(operation["move"])
                    if originals[destination] is not None:
                        raise ValueError("move destination already exists")
                    replacements[filename], replacements[destination] = (
                        None,
                        replacement,
                    )
                    modes[destination] = modes[filename]
                else:
                    replacements[filename] = replacement
        replacement_bytes = sum(
            len(value) for value in replacements.values() if value is not None
        )
        if total + replacement_bytes > MAX_TOTAL_BYTES:
            raise ValueError("patch working set exceeds 16 MiB")
    return originals, modes, replacements


def commit_changes(originals, modes, replacements):
    staged = {}
    backups = {}
    changed = []
    try:
        for filename, replacement in replacements.items():
            if replacement == originals[filename]:
                continue
            if replacement is not None:
                staged[filename] = stage_file(filename, replacement, modes[filename])
            if originals[filename] is not None:
                backups[filename] = stage_file(
                    filename, originals[filename], modes[filename]
                )

        # Revalidate every original before committing the first replacement.
        for filename, original in originals.items():
            if os.path.realpath(filename) != filename:
                raise ValueError(
                    "patch path changed during preparation; retry after inspection"
                )
            if original is None:
                if os.path.lexists(filename):
                    raise ValueError("patch destination was created during preparation")
            else:
                with open(filename, "rb") as source:
                    if source.read(MAX_FILE_BYTES + 1) != original:
                        raise ValueError(
                            "patch target changed during preparation; read it again"
                        )

        try:
            for filename in replacements:
                if replacements[filename] == originals[filename]:
                    continue
                if replacements[filename] is None:
                    os.unlink(filename)
                else:
                    os.replace(staged[filename], filename)
                changed.append(filename)
        except OSError as failure:
            incomplete = []
            for filename in reversed(changed):
                try:
                    if filename in backups:
                        os.replace(backups[filename], filename)
                    else:
                        os.unlink(filename)
                except OSError:
                    incomplete.append(filename)
            if incomplete:
                raise RuntimeError(
                    "patch write failed; rollback incomplete for: "
                    + ", ".join(incomplete)
                ) from failure
            raise OSError(
                "patch write failed; original files were restored"
            ) from failure
    finally:
        for filename in [*staged.values(), *backups.values()]:
            if os.path.exists(filename):
                os.unlink(filename)


def apply_patch(workspace, temporary_directory, patch):
    workspace = os.path.realpath(workspace)
    temporary_directory = os.path.realpath(temporary_directory)
    operations = parse_patch(patch)
    originals, modes, replacements = prepare_changes(
        workspace, temporary_directory, operations
    )

    commit_changes(originals, modes, replacements)

    changes = []
    for filename, content in replacements.items():
        original = originals[filename]
        before = text_snapshot(original or b"", exists=original is not None)
        after = text_snapshot(content or b"", exists=content is not None)
        changes.append(file_change(filename, before, after, workspace))
    return {
        "files": [
            {
                "path": change["path"],
                "kind": change["kind"],
                "added": change["added"],
                "removed": change["removed"],
            }
            for change in changes
        ],
        "_changes": changes,
    }


def main():
    workspace, temporary_directory = sys.argv[1:3]
    patch = sys.stdin.buffer.read(MAX_PATCH_BYTES + 1).decode("utf-8")
    print(json.dumps(apply_patch(workspace, temporary_directory, patch)))


if __name__ == "__main__":
    main()
