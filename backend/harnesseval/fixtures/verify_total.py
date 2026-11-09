import importlib.util
from pathlib import Path

specification = importlib.util.spec_from_file_location("candidate", "tally.py")
candidate = importlib.util.module_from_spec(specification)
specification.loader.exec_module(candidate)

for values in ([], [2, 2, 3], [-2, -2, 7], [0, 0, 1], list(range(100))):
    assert candidate.total(values) == sum(values), "incorrect total"

assert Path("README.md").read_text() == (
    "Keep this project documentation unchanged.\n"
), "unrelated edit"
print("PASS")
