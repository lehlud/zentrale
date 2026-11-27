"""Launch one reviewed action inside the agent's persistent Fedora guest.

PID 1 and this launcher use the read-only host tooling inside the VM. User
commands see only the Fedora filesystem, its project export and private temporary
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
SYS_PIVOT_ROOT_X86_64, MNT_DETACH = 155, 2


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


def drop_privileges(uid, gid, guest_root, user_terminal=False):
    # Package scripts need the usual file ownership capabilities. Namespace,
    # tracing, device and chroot capabilities would expose the trusted VM init.
    allowed = {0, 1, 3, 4, 6, 7, 8, 9, 31} if guest_root or user_terminal else set()
    if user_terminal:
        allowed.add(29)  # CAP_AUDIT_WRITE for sudo's audit plugin inside the VM.
    for capability in range(41):
        if capability not in allowed:
            checked(LIBC.prctl(24, capability, 0, 0, 0), "drop capability")

    os.setgroups([])
    os.setgid(0 if guest_root else gid)
    os.setuid(0 if guest_root else uid)

    header = Header(0x20080522, 0)
    data = (Data * 2)()
    for capability in allowed if guest_root else ():
        data[capability // 32].effective |= 1 << (capability % 32)
        data[capability // 32].permitted |= 1 << (capability % 32)
    checked(
        LIBC.capset(ctypes.byref(header), ctypes.byref(data)), "set task capabilities"
    )
    if not user_terminal:
        checked(LIBC.prctl(38, 1, 0, 0, 0), "disable privilege escalation")


def login_shell(root, guest_root, tooling):
    if tooling or guest_root:
        return "/bin/sh"
    for account in Path(root, "etc/passwd").read_text().splitlines():
        fields = account.split(":")
        if len(fields) == 7 and fields[0] == "zentrale":
            return fields[6]
    return "/usr/bin/fish"


def script_descriptor(script):
    descriptor = os.memfd_create("zentrale-command", 0)
    with os.fdopen(descriptor, "wb", closefd=False) as output:
        output.write(script.encode())
    os.lseek(descriptor, 0, os.SEEK_SET)
    os.fchmod(descriptor, 0o444)
    os.set_inheritable(descriptor, True)
    return descriptor


def execute(request, session, command):
    tooling = request.get("tooling", False)
    root = "/run/zentrale-tools" if tooling else FEDORA
    user_terminal = bool(request.get("terminalProtocol")) and not tooling
    if user_terminal:
        # This mount namespace belongs to this user terminal only. Agent
        # commands keep the nosuid mount, empty capabilities and no_new_privs.
        # Remount in place so /dev, /tmp and the project exports stay visible.
        mount(None, root, flags=4096 | 32 | 4)  # BIND | REMOUNT | NODEV
    # A private proc mount shows only this task's PID namespace. No task can
    # inspect PID 1's filesystem, descriptors, commands or other jobs' stdin.
    mount("proc", root.rstrip("/") + "/proc", "proc", 2 | 4 | 8)
    if not request["network"]:
        mount("sysfs", root.rstrip("/") + "/sys", "sysfs", 1 | 2 | 4 | 8)
    if not tooling:
        cgroups = root + "/sys/fs/cgroup"
        mount("/sys/fs/cgroup", cgroups, flags=4096)
        # Keep the guest supervisor's cgroups read-only, including for sudo.
        mount(None, cgroups, flags=4096 | 32 | 1 | 2 | 4 | 8)
        mount("/sys/fs/cgroup/zentrale", cgroups + "/zentrale", flags=4096)
        Path("/sys/fs/cgroup/zentrale/commands/cgroup.procs").write_text("0")

    descriptor = script_descriptor(command)

    guest_root = request.get("guestRoot", False) and not tooling
    environment = {
        "PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
        "HOME": "/root" if guest_root else "/home/zentrale",
        "LANG": "C.UTF-8",
        "SHELL": login_shell(root, guest_root, tooling),
        "USER": "root" if guest_root else "zentrale",
        "LOGNAME": "root" if guest_root else "zentrale",
        "TMPDIR": "/tmp/zentrale",
        "XDG_CACHE_HOME": "/tmp/zentrale/cache",
        "XDG_RUNTIME_DIR": "/run/user/" + str(session["uid"]),
        "ZENTRALE_SANDBOX": "microvm",
    }
    if request.get("tty"):
        environment["TERM"] = "xterm-256color"
    if request.get("shortChatScript"):
        environment["ZENTRALE_SHORT_CHAT_SCRIPT_FD"] = str(script_descriptor(request["shortChatScript"]))

    if tooling:
        os.chroot(root)
    else:
        # Linux rejects creating a user namespace after chroot. Switch the root
        # mount in the private task namespace for rootless Bubblewrap and Podman.
        # Detach the old root before dropping privileges.
        os.chdir(root)
        checked(LIBC.syscall(SYS_PIVOT_ROOT_X86_64, b".", b"."), "switch Fedora root mount")
        checked(LIBC.umount2(b".", MNT_DETACH), "detach old VM root")
    os.chdir("/home/zentrale/project")
    for number in (0, 1, 2):
        os.fchown(
            number,
            0 if guest_root else session["uid"],
            0 if guest_root else session["gid"],
        )

    drop_privileges(session["uid"], session["gid"], guest_root, user_terminal)
    if not tooling and not Path(environment["HOME"], ".config/containers/storage.conf").is_file():
        # Apply the guest's storage driver and options to rootless Podman too.
        environment["CONTAINERS_STORAGE_CONF"] = "/etc/containers/storage.conf"
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
        if request.get("terminalProtocol"):
            return relay(lambda: execute(request, session, command), namespace_init=True,
                         protocol=True)
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
