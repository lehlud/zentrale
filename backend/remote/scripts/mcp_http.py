"""Relay one HTTP request from inside the approved sandbox, without proxies.

The first stdout line carries status/headers; all remaining bytes are the HTTP
body, including SSE events. The Go MCP SDK owns framing and session negotiation.
"""

import base64
import http.client
import json
import sys
from urllib.parse import urlsplit


def main():
    request = json.loads(base64.b64decode(sys.argv[1]))
    address = urlsplit(request["url"])
    if address.scheme != "http" or address.hostname not in ("127.0.0.1", "::1", "localhost"):
        raise ValueError("MCP endpoint must be an HTTP loopback address inside the sandbox")
    connection = http.client.HTTPConnection(address.hostname, address.port, timeout=120)
    body = sys.stdin.buffer.read(1024 * 1024 + 1)
    if len(body) > 1024 * 1024:
        raise ValueError("MCP request exceeds 1 MiB")
    target = address.path or "/"
    if address.query:
        target += "?" + address.query
    headers = {key: ", ".join(values) for key, values in request["headers"].items()}
    try:
        connection.request(request["method"], target, body=body, headers=headers)
        response = connection.getresponse()
        json.dump({"status": response.status, "headers": response.getheaders()}, sys.stdout)
        sys.stdout.write("\n")
        sys.stdout.flush()
        while chunk := response.read1(65536):
            sys.stdout.buffer.write(chunk)
            sys.stdout.buffer.flush()
    finally:
        connection.close()


if __name__ == "__main__":
    main()
