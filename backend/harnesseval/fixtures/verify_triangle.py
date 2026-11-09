import hashlib
import importlib.util
from pathlib import Path

specification = importlib.util.spec_from_file_location("candidate", "compute.py")
candidate = importlib.util.module_from_spec(specification)
specification.loader.exec_module(candidate)

for value in (0, 1, 2, 10, 100):
    assert candidate.triangle(value) == value * (value + 1) // 2, (
        "incorrect triangle"
    )
assert hashlib.sha256(Path("build.py").read_bytes()).hexdigest() == (
    "EXPECTED_BUILD_SHA256"
), "progress fixture modified"
print("PASS")
