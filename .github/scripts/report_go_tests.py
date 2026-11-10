"""Turn failing go test -json records into safe GitHub annotations."""

import collections
import json
import sys


def annotations(lines):
    output = collections.defaultdict(str)
    for line in lines:
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if not isinstance(event, dict):
            continue
        key = (event.get("Package", ""), event.get("Test", ""))
        if event.get("Output"):
            output[key] = (output[key] + event["Output"])[-12000:]
        if event.get("Action") == "fail":
            message = " / ".join(filter(None, key)) + "\n" + output.pop(key, "")
            message = message.replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")
            yield "::error::" + message
        elif event.get("Action") == "pass":
            output.pop(key, None)


if __name__ == "__main__":
    with open(sys.argv[1], encoding="utf-8") as stream:
        for annotation in annotations(stream):
            print(annotation)
