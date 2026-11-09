#!/usr/bin/python3 -I
"""Minimal PID 1 for the managed guest; no host services or udev are started."""

import ctypes
import json
import os
import runpy
import re
from pathlib import Path
import subprocess
import sys
import time
import threading
import signal
import shutil
import socket


LIBC = ctypes.CDLL(None, use_errno=True)
READONLY, NOSUID, NODEV, NOEXEC, BIND = 1, 2, 4, 8, 4096
CONTROL = "/run/zentrale-control"


def mount(source, target, filesystem=None, flags=0, options=None):
    values = [
        value.encode() if value is not None else None
        for value in (source, target, filesystem, options)
    ]
    if (
        LIBC.mount(values[0], values[1], values[2], ctypes.c_ulong(flags), values[3])
        != 0
    ):
        raise OSError(ctypes.get_errno(), "mount " + target)


def initialize_filesystems():
    mount("proc", "/proc", "proc", NOSUID | NODEV | NOEXEC)
    mount("sysfs", "/sys", "sysfs", NOSUID | NODEV | NOEXEC)
    mount("devtmpfs", "/dev", "devtmpfs", NOSUID | NOEXEC)
    mount("tmpfs", "/run", "tmpfs", NOSUID | NODEV, "mode=0755")
    mount("tmpfs", "/tmp", "tmpfs", NOSUID | NODEV, "mode=1777")
    for target, filesystem, options in [
        ("/dev/shm", "tmpfs", "mode=1777"),
        ("/dev/pts", "devpts", "newinstance,ptmxmode=0666,mode=0620"),
    ]:
        os.makedirs(target, exist_ok=True)
        mount(filesystem, target, filesystem, NOSUID | NOEXEC, options)
    exports = []
    for part in Path("/proc/cmdline").read_text().split():
        match = re.fullmatch(r"virtme_initmount([0-9]+)=(/run/zentrale-(workspace|temporary|control|kernel|storage-[0-7]))", part)
        if match:
            exports.append((int(match[1]), match[2], match[3] == "kernel"))
    if not {"/run/zentrale-workspace", "/run/zentrale-temporary", CONTROL}.issubset({entry[1] for entry in exports}):
        raise RuntimeError("required VM exports are missing")
    for index, target, readonly in sorted(exports):
        os.makedirs(target, exist_ok=True)
        flags = NOSUID | NODEV | (READONLY if readonly else 0)
        mount(
            "virtme.initmount" + str(index),
            target,
            "9p",
            flags,
            "version=9p2000.L,trans=virtio,access=any,msize=524288,cache=none",
        )


def load_module(name):
    root = "/run/zentrale-kernel" if Path("/run/zentrale-kernel/lib/modules").is_dir() else "/"
    result = subprocess.run(
        ["/usr/sbin/modprobe", "--config", "/dev/null", "--dirname", root, "--", name],
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        raise RuntimeError("load guest module " + name + ": " + result.stderr.strip()[-2000:])


def discover_ports():
    load_module("virtio_console")
    expected = {
        "virtme.stdin",
        "virtme.stdout",
        "virtme.stderr",
        "virtme.dev_stdout",
        "virtme.dev_stderr",
        "virtme.ret",
    }
    ports = {}
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        for entry in Path("/sys/class/virtio-ports").glob("*"):
            name = (entry / "name").read_text(encoding="ascii").strip()
            device = Path("/dev") / entry.name
            if name in expected and device.exists():
                ports[name] = str(device)
        if expected == ports.keys():
            break
        time.sleep(0.02)
    else:
        raise RuntimeError(
            "MicroVM I/O ports did not appear: "
            + ", ".join(sorted(expected - ports.keys()))
        )
    directory = Path("/dev/virtio-ports")
    directory.mkdir(exist_ok=True)
    for name, device in ports.items():
        (directory / name).symlink_to(device)
    for name, destination in [
        ("stdin", "/proc/self/fd/0"),
        ("stdout", ports["virtme.dev_stdout"]),
        ("stderr", ports["virtme.dev_stderr"]),
    ]:
        target = Path("/dev") / name
        target.unlink(missing_ok=True)
        target.symlink_to(destination)
    return ports


def configure_network(enabled):
    busybox = CONTROL + "/busybox"
    subprocess.run([busybox, "ip", "link", "set", "lo", "up"], check=True)
    if not enabled:
        return
    load_module("virtio_net")
    deadline = time.monotonic() + 5
    while not Path("/sys/class/net/eth0").exists():
        if time.monotonic() >= deadline:
            raise RuntimeError("MicroVM network device did not appear")
        time.sleep(0.02)
    commands = [
        ["link", "set", "eth0", "up"],
        ["addr", "add", "10.0.2.15/24", "dev", "eth0"],
        ["route", "add", "default", "via", "10.0.2.2"],
    ]
    for arguments in commands:
        subprocess.run([busybox, "ip"] + arguments, check=True)
    resolver = Path("/run/zentrale-resolv.conf")
    resolver.write_text("nameserver 10.0.2.3\n", encoding="ascii")
    resolver.chmod(0o644)
    target = Path("/etc/resolv.conf").resolve()
    if not target.exists():
        target.parent.mkdir(parents=True, exist_ok=True)
        target.touch()
    mount(str(resolver), str(target), flags=BIND)


FEDORA = "/run/fedora"
GUEST_ENV = {"PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "HOME": "/root", "LANG": "C.UTF-8"}
STANDARD_PACKAGES = {
    "fish": ("fish",),
    "git": ("git",),
    "ripgrep": ("rg",),
    "python3": ("python3",),
    "python3-pip": ("pip3",),
    "curl": ("curl",),
    "openssh-clients": ("ssh", "scp"),
    "jq": ("jq",),
    "less": ("less",),
    "file": ("file",),
    "findutils": ("find", "xargs"),
    "diffutils": ("diff", "cmp"),
    "patch": ("patch",),
    "tar": ("tar",),
    "gzip": ("gzip",),
    "bzip2": ("bzip2",),
    "xz": ("xz",),
    "unzip": ("unzip",),
    "zip": ("zip",),
    "which": ("which",),
    "procps-ng": ("ps", "pgrep"),
    "util-linux-core": ("flock",),
    "util-linux-script": ("script",),
    "make": ("make",),
}


def prepare_fedora(request):
    load_module("virtio_blk")
    load_module("ext4")
    deadline = time.monotonic() + 10
    while not Path("/dev/vda").exists():
        if time.monotonic() >= deadline:
            raise RuntimeError("Fedora disk did not appear")
        time.sleep(0.05)
    if request.get("provision"):
        subprocess.run(["/usr/sbin/mkfs.ext4", "-q", "-F", "/dev/vda"], check=True)
    os.makedirs(FEDORA, exist_ok=True)
    mount("/dev/vda", FEDORA, "ext4", NOSUID | NODEV)
    if request.get("provision"):
        # The verified OCI archive is expanded only inside the VM. The host
        # never mounts a guest disk or executes an image's binaries.
        subprocess.run(
            [
                "/usr/bin/tar",
                "--extract",
                "--gzip",
                "--file=" + CONTROL + "/rootfs.tar.gz",
                "--directory=" + FEDORA,
            ],
            check=True,
        )
        (Path(CONTROL) / "provisioned").touch()
        return
    root = Path(FEDORA)
    for name in ("proc", "sys", "dev", "run", "tmp", "home/zentrale"):
        (root / name).mkdir(parents=True, exist_ok=True)
    mount("tmpfs", str(root / "run"), "tmpfs", NOSUID | NODEV, "mode=0755")
    mount("tmpfs", str(root / "tmp"), "tmpfs", NOSUID | NODEV, "mode=1777")
    mount("tmpfs", str(root / "dev"), "tmpfs", NOSUID | NOEXEC, "mode=0755")
    for name in ("null", "zero", "random", "urandom", "tty"):
        target = root / "dev" / name
        target.touch()
        mount("/dev/" + name, str(target), flags=BIND)
    (root / "dev/pts").mkdir()
    mount("devpts", str(root / "dev/pts"), "devpts", NOSUID | NOEXEC, "newinstance,ptmxmode=0666,mode=0620")
    (root / "dev/ptmx").symlink_to("pts/ptmx")
    (root / "dev/fd").symlink_to("/proc/self/fd")
    for number, name in enumerate(("stdin", "stdout", "stderr")):
        (root / "dev" / name).symlink_to("/proc/self/fd/" + str(number))
    mount("sysfs", str(root / "sys"), "sysfs", READONLY | NOSUID | NODEV | NOEXEC)
    for source, destination in [
        ("workspace", request["workspace"]),
        ("temporary", request["temporaryDirectory"]),
    ] + [("storage-" + str(index), entry["guestPath"]) for index, entry in enumerate(request.get("storage") or [])]:
        target = root / destination.lstrip("/")
        target.mkdir(parents=True, exist_ok=True)
        mount("/run/zentrale-" + source, str(target), flags=BIND)
    home = root / "home/zentrale"
    os.chown(home, request["uid"], request["gid"])
    passwd = root / "etc/passwd"
    if not any(
        line.startswith("zentrale:") for line in passwd.read_text().splitlines()
    ):
        with passwd.open("a") as output:
            output.write(
                "zentrale:x:{uid}:{gid}:Zentrale agent:/home/zentrale:/bin/bash\n".format(
                    **request
                )
            )
        with (root / "etc/group").open("a") as output:
            output.write("zentrale:x:{gid}:\n".format(**request))
    resolver = root / "etc/resolv.conf"
    resolver.unlink(missing_ok=True)
    resolver.write_text("nameserver 10.0.2.3\n")
    LIBC.prctl(4, 0, 0, 0, 0)  # PR_SET_DUMPABLE: task roots cannot inspect PID 1.



def enter_guest(root):
    def enter():
        os.chroot(root)
        os.chdir("/")
    return enter


def install_guest_packages(root, packages):
    manager = next((name for name in ("microdnf", "dnf5", "dnf")
                    if (root / "usr/bin" / name).is_file()), None)
    if manager is None:
        raise RuntimeError("Fedora package manager is missing; cannot install " + ", ".join(packages))
    mount("proc", str(root / "proc"), "proc", NOSUID | NODEV | NOEXEC)
    subprocess.run(
        ["/usr/bin/" + manager, "-y", "install", *packages],
        check=True, timeout=240, preexec_fn=enter_guest(root), env=GUEST_ENV,
    )


def missing_standard_packages(root):
    # Resolve commands inside the chroot too: a guest's absolute symlink must
    # never be mistaken for a working executable in the host tooling root.
    arguments = [item for package, commands in STANDARD_PACKAGES.items()
                 for command in commands for item in (package, command)]
    result = subprocess.run(
        ["/bin/sh", "-c", '''while [ "$#" -gt 0 ]; do
    package=$1; command=$2; shift 2
    command -v "$command" >/dev/null 2>&1 || printf '%s\\n' "$package"
done''', "standard-packages", *arguments],
        check=True, capture_output=True, text=True, timeout=15,
        preexec_fn=enter_guest(root), env=GUEST_ENV,
    )
    missing = set(result.stdout.splitlines())
    if missing.difference(STANDARD_PACKAGES):
        raise RuntimeError("invalid standard package probe result")
    return [package for package in STANDARD_PACKAGES if package in missing]


def prepare_standard_packages(root):
    missing = missing_standard_packages(root)
    if not missing:
        return
    # Keep working providers such as curl-minimal, rather than replacing them
    # just because their RPM name differs from the default package name.
    install_guest_packages(root, ["ca-certificates", *missing])
    remaining = missing_standard_packages(root)
    if remaining:
        raise RuntimeError("Fedora standard tools are still missing: " + ", ".join(remaining))


def prepare_user_shell(root):
    """Install the interactive shell in the guest, preserving its persistent disk."""
    fish = root / "usr/bin/fish"
    if not fish.is_file():
        install_guest_packages(root, ["fish"])
        if not fish.is_file():
            raise RuntimeError("Fedora fish installation did not provide /usr/bin/fish")
    passwd = root / "etc/passwd"
    accounts = passwd.read_text().splitlines()
    for index, account in enumerate(accounts):
        fields = account.split(":")
        # Migrate the previous default; retain a deliberately selected other shell.
        if len(fields) == 7 and fields[0] == "zentrale" and fields[6] in ("/bin/bash", "/bin/sh", "/usr/bin/bash", "/usr/bin/sh"):
            fields[6] = "/usr/bin/fish"
            accounts[index] = ":".join(fields)
    passwd.write_text("\n".join(accounts) + "\n")
    shells = root / "etc/shells"
    allowed = shells.read_text().splitlines() if shells.exists() else []
    if "/usr/bin/fish" not in allowed:
        shells.write_text("\n".join([*allowed, "/usr/bin/fish"]) + "\n")


def prepare_tooling(request):
    """Expose host Python inside a narrow guest filesystem, never host home/cache.

    This keeps fixed file operations available before Python is installed in
    Fedora. Even a project symlink race cannot expose SSH credentials or VM
    control files through the interpreter's filesystem view.
    """
    root = Path("/run/zentrale-tools")
    root.mkdir()
    mount("tmpfs", str(root), "tmpfs", NOSUID | NODEV, "mode=0755")
    for name in ("usr", "bin", "sbin", "lib", "lib64"):
        source = Path("/") / name
        if source.is_dir():
            target = root / name
            target.mkdir()
            mount(str(source), str(target), flags=BIND)
    for name in ("proc", "sys", "dev", "run", "tmp"):
        (root / name).mkdir()
    mount(FEDORA + "/sys", str(root / "sys"), flags=BIND)
    for name in ("null", "zero", "random", "urandom"):
        target = root / "dev" / name
        target.touch()
        mount("/dev/" + name, str(target), flags=BIND)
    (root / "dev/fd").symlink_to("/proc/self/fd")
    for number, name in enumerate(("stdin", "stdout", "stderr")):
        (root / "dev" / name).symlink_to("/proc/self/fd/" + str(number))
    for source, destination in [
        ("workspace", request["workspace"]),
        ("temporary", request["temporaryDirectory"]),
    ] + [("storage-" + str(index), entry["guestPath"]) for index, entry in enumerate(request.get("storage") or [])]:
        target = root / destination.lstrip("/")
        target.mkdir(parents=True, exist_ok=True)
        mount("/run/zentrale-" + source, str(target), flags=BIND)


def save_status(job, code):
    temporary = job / "status.new"
    temporary.write_text(str(code))
    os.replace(temporary, job / "status")


def run_job(job):
    process = None
    try:
        request = json.loads((job / "request.json").read_text())
        MailboxStream = runpy.run_path(str(Path(CONTROL, "streams.py")))["MailboxStream"]
        cancelled = lambda: (job / "cancel").exists() or Path(CONTROL, "stop").exists()
        process = subprocess.Popen(
            ["/usr/bin/python3", "-I", CONTROL + "/guest.py", str(job)],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            start_new_session=True,
        )

        def input_stream():
            try:
                if request.get("protocolIO"):
                    stream = MailboxStream(job, "stdin", cancelled)
                    while process.poll() is None:
                        data = stream.read()
                        if data is None:
                            if cancelled():
                                break
                            time.sleep(0.005)
                        elif not data:
                            break
                        else:
                            process.stdin.write(data)
                            process.stdin.flush()
                    return
                with (job / "stdin").open("rb") as source:
                    while process.poll() is None:
                        data = source.read(65536)
                        if data:
                            process.stdin.write(data)
                            process.stdin.flush()
                        elif (job / "stdin.eof").exists():
                            break
                        else:
                            time.sleep(0.03)
            except (BrokenPipeError, OSError):
                pass
            finally:
                process.stdin.close()

        def output_stream(source, name):
            output = MailboxStream(job, name, cancelled)
            try:
                while data := source.read1(65536):
                    output.write(data)
            except OSError:
                pass  # Cancellation must release a writer under backpressure.
            finally:
                output.close()
                source.close()

        threads = [threading.Thread(target=input_stream, daemon=True)]
        for name in ("stdout", "stderr"):
            threads.append(
                threading.Thread(
                    target=output_stream,
                    args=(getattr(process, name), name),
                    daemon=True,
                )
            )
        for thread in threads:
            thread.start()
        while process.poll() is None:
            if (job / "cancel").exists() or Path(CONTROL, "stop").exists():
                # The launcher forwards this to the PID-namespace init. Killing
                # that init also removes descendants that changed process groups.
                process.send_signal(signal.SIGTERM)
                try:
                    process.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    process.kill()
                break
            time.sleep(0.05)
        code = process.wait()
        for thread in threads[1:]:
            thread.join()
        save_status(job, code if code >= 0 else 128 - code)
    except BaseException as error:
        (job / "stderr").write_text("MicroVM task failed: " + str(error))
        save_status(job, 125)
    finally:
        if process is not None and process.poll() is None:
            process.kill()
            process.wait()


def serve_jobs(request):
    prepare_fedora(request)
    if request.get("provision"):
        return
    socket.sethostname("zentrale-microvm")
    configure_network(True)
    prepare_standard_packages(Path(FEDORA))
    prepare_user_shell(Path(FEDORA))
    prepare_tooling(request)
    proof = {
        "os": "fedora",
        "version": "45",
        "bootId": Path("/proc/sys/kernel/random/boot_id").read_text().strip(),
        "vmType": request.get("vmType", "microvm"),
        "gpuDevices": request.get("gpuDevices", []),
        "storage": [{"id": entry["id"], "path": entry["guestPath"]} for entry in request.get("storage") or []],
    }
    if proof["gpuDevices"]:
        proof["guestGPUs"] = guest_gpus()
        if not proof["guestGPUs"]:
            raise RuntimeError("the q35 guest did not enumerate a passed-through GPU")
    temporary = Path(CONTROL, "ready.new")
    temporary.write_text(json.dumps(proof))
    os.replace(temporary, Path(CONTROL, "ready.json"))
    workers = {}
    while not Path(CONTROL, "stop").exists():
        for job in Path(CONTROL, "requests").iterdir():
            if (
                (job / "started").exists()
                and job.name not in workers
                and not (job / "status").exists()
            ):
                worker = threading.Thread(target=run_job, args=(job,), daemon=True)
                workers[job.name] = worker
                worker.start()
            worker = workers.get(job.name)
            if (job / "acknowledged").exists() and (
                worker is None or not worker.is_alive()
            ):
                shutil.rmtree(job)
                workers.pop(job.name, None)
        time.sleep(0.05)
    for worker in workers.values():
        worker.join(timeout=5)


def guest_gpus():
    devices = []
    for device in Path("/sys/bus/pci/devices").iterdir():
        if int((device / "class").read_text().strip(), 16) >> 16 == 0x03:
            driver = device / "driver"
            devices.append({
                "address": device.name,
                "vendor": (device / "vendor").read_text().strip(),
                "device": (device / "device").read_text().strip(),
                "driver": driver.resolve().name if driver.exists() else "none",
            })
    return devices


def main():
    ports = {}
    status = 125
    try:
        initialize_filesystems()
        ports = discover_ports()
        for number, name in [(1, "virtme.stdout"), (2, "virtme.stderr")]:
            descriptor = os.open(ports[name], os.O_WRONLY)
            os.dup2(descriptor, number)
            os.close(descriptor)
        request = json.loads(Path(CONTROL, "request.json").read_text())
        serve_jobs(request)
        status = 0
    except BaseException as error:
        print(
            "MicroVM guest initialization failed: " + str(error),
            file=sys.stderr,
            flush=True,
        )
    finally:
        sys.stdout.flush()
        sys.stderr.flush()
        if "virtme.ret" in ports:
            with open(ports["virtme.ret"], "w") as result:
                result.write(str(status) + "\n")
        os.sync()
        LIBC.reboot(0x4321FEDC)


if __name__ == "__main__":
    main()
