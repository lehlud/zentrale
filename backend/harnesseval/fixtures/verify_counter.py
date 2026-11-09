from pathlib import Path

assert Path("result.txt").read_text() == "7\n", (
    "interactive result missing or incorrect"
)
print("PASS")
