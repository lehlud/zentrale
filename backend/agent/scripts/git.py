"""Use a source-specific deployment key for one approved Git interaction."""
import json
import os
import shlex
import shutil
import subprocess
import sys
import tempfile


def write_secret(directory, filename, content):
    destination = os.path.join(directory, filename)
    with open(destination, "w") as target:
        target.write(content)
    os.chmod(destination, 0o600)
    return destination


def disable_push(directory):
    subprocess.run(
        ["git", "-C", directory, "remote", "set-url", "--push", "origin", "DISABLED_READ_ONLY"],
        check=True,
    )
    hook = os.path.join(directory, ".git", "hooks", "pre-push")
    with open(hook, "w") as target:
        target.write('#!/bin/sh\necho "Read-only source" >&2\nexit 1\n')
    os.chmod(hook, 0o700)


def main():
    configuration = json.load(sys.stdin)
    temporary_directory = tempfile.mkdtemp(prefix="git-", dir=configuration["temporaryDirectory"])
    os.chmod(temporary_directory, 0o700)
    try:
        key = write_secret(temporary_directory, "key", configuration["key"])
        known_hosts = write_secret(temporary_directory, "known_hosts", configuration["known"])
        environment = dict(os.environ)
        environment["GIT_SSH_COMMAND"] = (
            "ssh -i " + shlex.quote(key)
            + " -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="
            + shlex.quote(known_hosts)
        )
        directory = configuration["directory"]
        if configuration["operation"] == "clone":
            os.makedirs(os.path.dirname(directory), exist_ok=True)
            arguments = ["git", "clone", "--", configuration["url"], directory]
        else:
            arguments = ["git", "-C", directory, *configuration["arguments"]]
        subprocess.run(arguments, env=environment, check=True)
        if configuration["readOnly"] and configuration["operation"] == "clone":
            disable_push(directory)
        print(json.dumps({"directory": directory, "readOnly": configuration["readOnly"]}))
    finally:
        shutil.rmtree(temporary_directory)


if __name__ == "__main__":
    main()
