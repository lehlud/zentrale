// A display fixture only: neither its Python code nor its HTTP endpoint is run.
export const pythonHereDoc = `TMPDIR=/home/zentrale/scan-tmp /home/zentrale/scan-venv/bin/python - <<'PY'
import asyncio

from mcp import ClientSession
from mcp.client.streamable_http import streamablehttp_client

async def check():
    async with streamablehttp_client('http://127.0.0.1:8766/mcp') as (read, write, _):
        async with ClientSession(read, write) as session:
            await session.initialize()
            result = await session.call_tool('health', {})
            print(result, flush=True)

asyncio.run(asyncio.wait_for(check(), timeout=15))
PY
`;
