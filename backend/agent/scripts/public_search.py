"""Run one bounded search using the configured DDGS runtime."""

import json
import sys

from ddgs import DDGS

query = sys.stdin.read(513).strip()
if not query or len(query.encode("utf-8")) > 512:
    raise SystemExit(1)

# Use only the selected search engine, rather than sending each query to the
# entire set of engines available in DDGS's auto mode.
entries = DDGS(timeout=7).text(query, region="de-de", backend="yahoo", max_results=8)

print(json.dumps([
    {
        "title": entry.get("title", "")[:400],
        "url": entry.get("href", "")[:4096],
        "snippet": entry.get("body", "")[:1000],
    }
    for entry in entries[:8]
]))
