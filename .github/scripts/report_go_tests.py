"""Turn failing go test -json records into safe GitHub annotations."""

import collections
import json
import sys


def annotations(lines):
    output = collections.defaultdict(str)
    diagnostics = {}
    for line in lines:
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if not isinstance(event, dict):
            continue
        key = (event.get("Package", ""), event.get("Test", ""))
        if event.get("Output"):
            text = event["Output"]
            package_key = (key[0], "")
            output[package_key] = (output[package_key] + text)[-12000:]
            if key[1]:
                output[key] = (output[key] + text)[-12000:]
            if any(marker in text for marker in ("panic:", "fatal error:", "WARNING: DATA RACE")):
                diagnostics.setdefault(key[0], "")
            if key[0] in diagnostics:
                diagnostics[key[0]] = (diagnostics[key[0]] + text)[:12000]
        if event.get("Action") == "fail":
            details = output.pop(key, "")
            if not key[1]:
                details = diagnostics.pop(key[0], details)
            message = " / ".join(filter(None, key)) + "\n" + details
            message = message.replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")
            yield "::error::" + message
        elif event.get("Action") == "pass":
            output.pop(key, None)


if __name__ == "__main__":
    with open(sys.argv[1], encoding="utf-8") as stream:
        for annotation in annotations(stream):
            print(annotation)
