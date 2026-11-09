import sys
from pathlib import Path

value = 0
print("READY", flush=True)
for line in sys.stdin:
    if line.strip() == "quit":
        Path("result.txt").write_text(str(value) + "\n")
        print("BYE", flush=True)
        break
    value += int(line)
    print("SUM=" + str(value), flush=True)
