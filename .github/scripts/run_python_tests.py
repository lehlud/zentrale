"""Run the unchanged unittest CLI and expose failures in GitHub annotations."""

import subprocess
import sys


def failure_annotation(output):
    # Keep the useful traceback tail, bounded and escaped as ONE workflow command.
    message = output[-12000:].replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")
    return "::error title=Python test failure::" + message


def main(arguments):
    try:
        result = subprocess.run(
            [sys.executable, "-m", "unittest", *arguments],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=300,
        )
    except subprocess.TimeoutExpired:
        print(failure_annotation("Python unittest exceeded its 300 second deadline"))
        return 1
    print(result.stdout, end="", flush=True)
    if result.returncode:
        print(failure_annotation(result.stdout or "Python unittest failed without output"))
    return result.returncode


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
