"""Fetch a document on the remote machine and return text or a remote temp file."""
import html.parser
import json
import os
import sys
import tempfile
import urllib.request

MAXIMUM_RESPONSE_BYTES = 8 * 1024 * 1024
INLINE_CHARACTER_LIMIT = 24000


class DocumentText(html.parser.HTMLParser):
    def __init__(self):
        super().__init__()
        self.parts = []
        self.hidden_depth = 0

    def handle_starttag(self, tag, attributes):
        if tag in ("script", "style"):
            self.hidden_depth += 1
        if tag in ("p", "div", "br", "li", "h1", "h2", "h3"):
            self.parts.append("\n")
        if tag == "a":
            destination = dict(attributes).get("href", "")
            if destination.startswith(("http://", "https://", "//")):
                self.parts.append(" " + destination + " ")

    def handle_endtag(self, tag):
        if tag in ("script", "style"):
            self.hidden_depth = max(0, self.hidden_depth - 1)

    def handle_data(self, text):
        if not self.hidden_depth:
            self.parts.append(text)


def fetch_document(address, raw):
    request = urllib.request.Request(address, headers={"User-Agent": "Zentrale/1.0"})
    with urllib.request.urlopen(request, timeout=30) as response:
        body = response.read(MAXIMUM_RESPONSE_BYTES + 1)
        if len(body) > MAXIMUM_RESPONSE_BYTES:
            raise ValueError("response exceeds 8 MiB")
        content = body.decode(response.headers.get_content_charset() or "utf-8", errors="replace")
        if not raw and "html" in response.headers.get("Content-Type", ""):
            parser = DocumentText()
            parser.feed(content)
            content = "".join(parser.parts)
        return response.url, content


def main():
    address, raw, temporary_directory = sys.argv[1:]
    resolved_address, content = fetch_document(address, raw == "true")
    result = {"url": resolved_address}
    if len(content) > INLINE_CHARACTER_LIMIT:
        descriptor, filename = tempfile.mkstemp(
            prefix="fetch-", suffix=".txt", dir=temporary_directory
        )
        with os.fdopen(descriptor, "w") as target:
            target.write(content)
        result.update(path=filename, characters=len(content), preview=content[:1000])
    else:
        result["content"] = content
    print(json.dumps(result))


if __name__ == "__main__":
    main()
