"""Launch one reviewed action inside the agent's persistent Fedora guest.

PID 1 and this launcher use the read-only host tooling inside the VM. User
commands see only the Fedora chroot, its project export and private temporary
export. They cannot reach the supervisor mailbox or the host's root filesystem.
"""

import ctypes
import json
import os
import runpy
from pathlib import Path
import resource
import signal
import sys
import subprocess

LIBC = ctypes.CDLL(None, use_errno=True)
CONTROL = "/run/zentrale-control"
FEDORA = "/run/fedora"
CLONE_NEWNS, CLONE_NEWPID, CLONE_NEWNET = 0x20000, 0x20000000, 0x40000000


class Header(ctypes.Structure):
    _fields_ = [("version", ctypes.c_uint32), ("pid", ctypes.c_int)]


class Data(ctypes.Structure):
    _fields_ = [
        ("effective", ctypes.c_uint32),
        ("permitted", ctypes.c_uint32),
        ("inheritable", ctypes.c_uint32),
    ]


def checked(result, description):
    if result != 0:
        raise OSError(ctypes.get_errno(), description)


def mount(source, target, filesystem=None, flags=0):
    values = [
        value.encode() if value is not None else None
        for value in (source, target, filesystem)
    ]
    checked(LIBC.mount(*values, ctypes.c_ulong(flags), None), "mount " + target)


def drop_privileges(uid, gid, guest_root):
    # Package scripts need the usual file ownership capabilities. Namespace,
    # tracing, device and chroot capabilities would expose the trusted VM init.
    allowed = {0, 1, 3, 4, 6, 7, 8, 9, 31} if guest_root else set()
    for capability in range(41):
        if capability not in allowed:
            checked(LIBC.prctl(24, capability, 0, 0, 0), "drop capability")
    os.setgroups([])
    os.setgid(0 if guest_root else gid)
    os.setuid(0 if guest_root else uid)

    header = Header(0x20080522, 0)
    data = (Data * 2)()
    for capability in allowed:
        data[capability // 32].effective |= 1 << (capability % 32)
        data[capability // 32].permitted |= 1 << (capability % 32)
    checked(
        LIBC.capset(ctypes.byref(header), ctypes.byref(data)), "set task capabilities"
    )
    checked(LIBC.prctl(38, 1, 0, 0, 0), "disable privilege escalation")



def login_shell(root, guest_root, tooling):
    if tooling or guest_root:
        return "/bin/sh"
    for account in Path(root, "etc/passwd").read_text().splitlines():
        fields = account.split(":")
        if len(fields) == 7 and fields[0] == "zentrale":
            return fields[6]
    return "/usr/bin/fish"


def execute(request, session, command):
    tooling = request.get("tooling", False)
    root = "/run/zentrale-tools" if tooling else FEDORA
    # A private proc mount shows only this task's PID namespace. No task can
    # inspect PID 1's filesystem, descriptors, commands or other jobs' stdin.
    mount("proc", root.rstrip("/") + "/proc", "proc", 2 | 4 | 8)
    if not request["network"]:
        mount("sysfs", root.rstrip("/") + "/sys", "sysfs", 1 | 2 | 4 | 8)
    descriptor = os.memfd_create("zentrale-command", 0)
    os.write(descriptor, command.encode())
    os.lseek(descriptor, 0, os.SEEK_SET)
    os.fchmod(descriptor, 0o444)
    os.set_inheritable(descriptor, True)
    guest_root = request.get("guestRoot", False) and not tooling
    environment = {
        "PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
        "HOME": "/root" if guest_root else "/home/zentrale",
        "LANG": "C.UTF-8",
        "SHELL": login_shell(root, guest_root, tooling),
        "USER": "root" if guest_root else "zentrale",
        "LOGNAME": "root" if guest_root else "zentrale",
        "TMPDIR": session["temporaryDirectory"],
        "XDG_CACHE_HOME": session["temporaryDirectory"] + "/cache",
        "ZENTRALE_SANDBOX": "microvm",
    }
    if request.get("tty"):
        environment["TERM"] = "xterm-256color"
    os.chroot(root)
    os.chdir(session["workspace"])
    for number in (0, 1, 2):
        os.fchown(
            number,
            0 if guest_root else session["uid"],
            0 if guest_root else session["gid"],
        )
    drop_privileges(session["uid"], session["gid"], guest_root)
    resource.setrlimit(resource.RLIMIT_NPROC, (256, 256))
    os.execve("/bin/sh", ["/bin/sh", "/proc/self/fd/" + str(descriptor)], environment)


def main():
    job = Path(sys.argv[1])
    request = json.loads((job / "request.json").read_text())
    session = json.loads(Path(CONTROL, "request.json").read_text())
    command = (job / "command.sh").read_text()
    flags = CLONE_NEWNS
    if not request["network"]:
        flags |= CLONE_NEWNET
    checked(LIBC.unshare(flags), "isolate task namespaces")
    if not request["network"]:
        subprocess.run(
            [CONTROL + "/busybox", "ip", "link", "set", "lo", "up"], check=True
        )
    mount(None, "/", flags=16384 | 262144)  # MS_REC | MS_PRIVATE
    checked(LIBC.unshare(CLONE_NEWPID), "isolate task processes")
    if request.get("tty"):
        relay = runpy.run_path(CONTROL + "/terminal.py")["run_terminal"]
        return relay(lambda: execute(request, session, command), namespace_init=True)
    child = os.fork()
    if child == 0:
        execute(request, session, command)

    def stop(signum, frame):
        try:
            os.kill(child, signal.SIGKILL)
        except ProcessLookupError:
            pass

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGHUP, stop)
    _, status = os.waitpid(child, 0)
    code = os.waitstatus_to_exitcode(status)
    return code if code >= 0 else 128 - code


if __name__ == "__main__":
    sys.exit(main())
