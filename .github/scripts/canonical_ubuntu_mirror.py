"""Use Ubuntu's canonical archive when the runner's Azure mirror has DNS issues.

Only the mirror hostname changes. Suites, components, protocols and Signed-By
settings (and the separate security archive) remain untouched.
"""

import pathlib
import sys


def canonicalize(filename):
    path = pathlib.Path(filename)
    if not path.exists():
        return
    original = path.read_text()
    updated = original.replace("http://azure.archive.ubuntu.com/", "http://archive.ubuntu.com/").replace(
        "https://azure.archive.ubuntu.com/", "https://archive.ubuntu.com/"
    )
    if updated != original:
        path.write_text(updated)


if __name__ == "__main__":
    for filename in sys.argv[1:]:
        canonicalize(filename)
