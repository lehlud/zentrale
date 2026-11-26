"""Run one bounded search using the configured DDGS runtime."""

import json
import sys

from ddgs import DDGS
from ddgs.exceptions import DDGSException

query = sys.stdin.read(513).strip()
if not query or len(query.encode("utf-8")) > 512:
    raise SystemExit(1)

# Use only the selected search engine, rather than sending each query to the
# entire set of engines available in DDGS's auto mode.
try:
    entries = DDGS(timeout=7).text(query, region="de-de", backend="yahoo", max_results=8)
except DDGSException as failure:
    # DDGS 9.16 reports an empty result as this exact base exception. Keep
    # timeouts, rate limits and other provider failures as errors.
    if type(failure) is not DDGSException or str(failure) != "No results found.":
        raise
    entries = []

print(json.dumps([
    {
        "title": entry.get("title", "")[:400],
        "url": entry.get("href", "")[:4096],
        "snippet": entry.get("body", "")[:1000],
    }
    for entry in entries[:8]
]))
