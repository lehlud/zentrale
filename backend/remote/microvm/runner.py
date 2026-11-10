"""Managed, unprivileged QEMU/KVM runner. Only this trusted code runs on the host.

virtme-ng supplies the guest boot and I/O protocol. Its pinned wheel is kept
unmodified, with its license, outside every guest-writable filesystem export.
"""

import ctypes
import fcntl
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import re
import runpy
import secrets
import shutil
import signal
import stat
import subprocess
import sys
import socket
import tarfile
import tempfile
import time
import urllib.request
import zipfile
import threading
import traceback


UNAVAILABLE = 78
SYSTEM_PATH = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
MAX_DOWNLOAD_BYTES = 8 * 1024 * 1024
MAX_KERNEL_EXTRACT_BYTES = 256 * 1024 * 1024


class UnavailableError(RuntimeError):
    """No supported local VM prerequisites; auto may select Bubblewrap."""


def vm_options(configuration):
    vm_type = configuration.get("vmType", "microvm")
    addresses = configuration.get("gpuDevices", [])
    if vm_type not in ("microvm", "q35"):
        raise RuntimeError("vm_type must be microvm or q35")
    if not isinstance(addresses, list) or len(addresses) > 16:
        raise RuntimeError("gpu_devices must be a list of at most 16 PCI addresses")
    if addresses and vm_type != "q35":
        raise RuntimeError("GPU passthrough requires vm_type q35")
    return vm_type, addresses


def vfio_helpers(configuration):
    namespace = {"__name__": "zentrale_vfio"}
    exec(configuration["vfio"], namespace)
    return namespace


def storage_mounts(execution, configured=None):
    mounts = execution.get("storage") or []
    if not isinstance(mounts, list) or len(mounts) > 8:
        raise RuntimeError("invalid project storage mounts")

    ids = set()
    for entry in mounts:
        identifier = entry.get("id", "")
        source = Path(entry.get("hostPath", ""))
        if (
            not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,79}", identifier)
            or identifier in ids
        ):
            raise RuntimeError("invalid project storage id")
        ids.add(identifier)
        if entry.get("guestPath") != "/mnt/zentrale-storage/" + identifier:
            raise RuntimeError("invalid project storage guest path")
        if (
            not source.is_absolute()
            or source.is_symlink()
            or source.name != "project-" + execution["projectId"]
            or not source.is_dir()
        ):
            raise RuntimeError(
                "project storage must be an existing managed project directory"
            )

    if configured is not None and ids != {entry["id"] for entry in configured}:
        raise RuntimeError("configured project storage has not been prepared")
    return mounts


def prerequisites(configuration=None):
    configuration = configuration or {}
    vm_type, addresses = vm_options(configuration)
    if platform.system() != "Linux" or platform.machine() != "x86_64":
        raise UnavailableError("managed MicroVMs currently require Linux x86_64")
    if sys.version_info < (3, 10):
        raise UnavailableError("managed MicroVMs require Python 3.10 or newer")

    try:
        descriptor = os.open("/dev/kvm", os.O_RDWR | os.O_CLOEXEC)
        try:
            if fcntl.ioctl(descriptor, 0xAE00, 0) != 12:
                raise UnavailableError("unsupported KVM API")
        finally:
            os.close(descriptor)
    except OSError as error:
        raise UnavailableError("KVM cannot be opened: " + str(error)) from error

    for name in (
        "qemu-system-x86_64",
        "qemu-img",
        "mkfs.ext4",
        "tar",
        "modprobe",
        "bash",
        "mount",
    ):
        if shutil.which(name, path=SYSTEM_PATH) is None:
            raise UnavailableError("required MicroVM command is missing: " + name)

    machines = subprocess.run(
        [shutil.which("qemu-system-x86_64", path=SYSTEM_PATH), "-machine", "help"],
        check=True,
        capture_output=True,
        text=True,
        timeout=10,
    )
    if not any(line.startswith(vm_type + " ") for line in machines.stdout.splitlines()):
        raise UnavailableError(
            "installed QEMU does not provide the " + vm_type + " machine"
        )

    if addresses:
        features = subprocess.run(
            [shutil.which("qemu-system-x86_64", path=SYSTEM_PATH), "-device", "help"],
            check=True,
            capture_output=True,
            text=True,
            timeout=10,
        )
        if 'name "vfio-pci"' not in features.stdout:
            raise RuntimeError(
                "installed QEMU does not provide vfio-pci GPU passthrough"
            )
        vfio_helpers(configuration)["inspect_devices"](addresses, probe_groups=False)
    return compatible_host_kernel(vm_type)


def compatible_host_kernel(vm_type="microvm"):
    version = platform.release()
    kernel = Path("/boot/vmlinuz-" + version)
    if not kernel.is_file() or not os.access(kernel, os.R_OK):
        return None

    # Drivers alone do not guarantee the namespaces used by reviewed commands.
    # When a distro exposes its config, reject explicitly disabled features too.
    config = Path("/boot/config-" + version)
    try:
        if config.is_file():
            settings = config.read_text(encoding="ascii")
        elif Path("/proc/config.gz").is_file():
            with gzip.open("/proc/config.gz", "rt", encoding="ascii") as stream:
                settings = stream.read()
        else:
            settings = ""
    except OSError:
        settings = ""

    required = [
        "NAMESPACES",
        "PID_NS",
        "NET_NS",
        "DEVTMPFS",
        "TMPFS",
        "PROC_FS",
        "SYSFS",
    ]
    if vm_type == "q35":
        required += ["PCI", "PCI_MSI"]
    for name in required:
        if "# CONFIG_" + name + " is not set" in settings:
            return None

    try:
        if vm_type == "microvm":
            check_guest_kernel_modules()
        else:
            check_guest_kernel_modules(vm_type=vm_type)
    except RuntimeError:
        return None
    return kernel


def check_guest_kernel_modules(root=Path("/"), version=None, vm_type="microvm"):
    # A readable kernel and KVM alone do not establish that the guest can boot.
    # Probe without loading modules or executing host modprobe install hooks.
    modprobe = shutil.which("modprobe", path=SYSTEM_PATH)
    for module in (
        "9p",
        "9pnet_virtio",
        "virtio_pci" if vm_type == "q35" else "virtio_mmio",
        "virtio_console",
        "virtio_blk",
        "ext4",
        "virtio_net",
    ):
        result = subprocess.run(
            [
                modprobe,
                "--config",
                "/dev/null",
                "--dirname",
                str(root),
                "--set-version",
                version or platform.release(),
                "--dry-run",
                "--show-depends",
                "--",
                module,
            ],
            capture_output=True,
            text=True,
            timeout=5,
        )
        if result.returncode != 0:
            raise RuntimeError(
                "guest kernel "
                + (version or platform.release())
                + " cannot run managed MicroVMs: required kernel support is missing: "
                + module
                + "\n"
                + result.stderr.strip()[-1000:]
            )


def within(directory, candidate):
    return candidate == directory or directory in candidate.parents


def private_directory(directory):
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    metadata = directory.lstat()
    if (
        not stat.S_ISDIR(metadata.st_mode)
        or metadata.st_uid != os.getuid()
        or metadata.st_mode & 0o077
    ):
        raise RuntimeError(
            "MicroVM cache must be a private directory owned by the SSH user: "
            + str(directory)
        )
    return directory.resolve()


def cache_directory(writable_roots=()):
    # One location per SSH user protects concurrent projects too. Choosing a
    # different cache for a broad workspace would expose another VM's cache.
    candidate = Path("/var/tmp") / ("zentrale-microvm-" + str(os.getuid()))
    cache = private_directory(candidate)
    if any(within(root, cache) for root in writable_roots):
        raise RuntimeError(
            "the writable workspace contains the protected MicroVM cache: " + str(cache)
        )
    return cache


def download(artifact, limit=None):
    limit = MAX_DOWNLOAD_BYTES if limit is None else limit
    with urllib.request.urlopen(artifact["url"], timeout=60) as response:
        data = response.read(limit + 1)
    if len(data) > limit or hashlib.sha256(data).hexdigest() != artifact["sha256"]:
        raise RuntimeError("MicroVM runtime download failed SHA-256 verification")
    return data


def install_kernel(cache, artifact):
    """Install a guest-only kernel, never host packages or host modules."""
    version = artifact["version"]
    if not re.fullmatch(r"[A-Za-z0-9_.+-]+", version) or version in (".", ".."):
        raise RuntimeError("invalid managed guest kernel version")
    destination = cache / ("kernel-" + version + "-" + artifact["sha256"][:12] + "-v1")
    kernel = destination / "boot" / ("vmlinuz-" + version)
    marker = destination / "installed.json"
    installed = {"artifact": artifact, "layout": "uncompressed-modules-v1"}
    if (
        marker.is_file()
        and kernel.is_file()
        and json.loads(marker.read_text()) == installed
    ):
        return kernel

    with tempfile.TemporaryDirectory(prefix="kernel-install-", dir=cache) as temporary:
        staging = Path(temporary)
        # Expose both paths for hosts whose kmod uses /usr/lib/modules.
        (staging / "usr/lib/modules").mkdir(parents=True)
        (staging / "lib").symlink_to("usr/lib", target_is_directory=True)
        data = download(artifact, limit=64 * 1024 * 1024)
        with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as package:
            extracted = 0
            decompressed = False
            for index, member in enumerate(package):
                if index >= 20000:
                    raise RuntimeError("guest kernel archive has too many members")
                path = Path(member.name)
                if path.is_absolute() or ".." in path.parts:
                    raise RuntimeError("unsafe path in the guest kernel archive")
                if member.name == artifact["member"]:
                    target = staging / "boot" / ("vmlinuz-" + version)
                elif member.name == artifact["config"]:
                    target = staging / "boot" / ("config-" + version)
                elif path.parts[:3] == ("lib", "modules", version):
                    target = staging / "usr" / path
                else:
                    continue
                if member.isdir():
                    target.mkdir(parents=True, exist_ok=True)
                    continue
                if not member.isfile() or not within(staging, target.resolve()):
                    raise RuntimeError("unsafe member in the guest kernel archive")
                compressed_module = target.name.endswith(".ko.gz")
                if compressed_module:
                    target = target.with_suffix("")
                    decompressed = True
                target.parent.mkdir(parents=True, exist_ok=True)
                with package.extractfile(member) as source, target.open("xb") as output:
                    stream = (
                        gzip.GzipFile(fileobj=source) if compressed_module else source
                    )
                    with stream:
                        while chunk := stream.read(1024 * 1024):
                            extracted += len(chunk)
                            if extracted > MAX_KERNEL_EXTRACT_BYTES:
                                raise RuntimeError(
                                    "guest kernel archive exceeds its extraction limit"
                                )
                            output.write(chunk)
                target.chmod(0o600)

        # Validate the staged kernel before publishing it to the shared cache.
        if not (staging / "boot" / kernel.name).is_file():
            raise RuntimeError("guest kernel image is missing from the archive")
        if (
            decompressed
            or not (staging / "lib/modules" / version / "modules.dep").is_file()
        ):
            depmod = shutil.which("depmod", path=SYSTEM_PATH)
            if depmod is None:
                raise RuntimeError(
                    "depmod is required to prepare the fallback guest kernel"
                )
            subprocess.run(
                [depmod, "--basedir", str(staging), version], check=True, timeout=30
            )
        check_guest_kernel_modules(staging, version)

        (staging / "installed.json").write_text(json.dumps(installed), encoding="utf-8")
        if destination.exists():
            shutil.rmtree(destination)
        os.rename(staging, destination)
    return kernel


def install_runtime(cache, release):
    destination = cache / release["version"]
    marker = destination / "installed.json"
    if marker.is_file():
        installed = json.loads(marker.read_text(encoding="utf-8"))
        if all(
            installed.get(key) == release[key]
            for key in ("version", "runtime", "busybox")
        ):
            return destination
    with tempfile.TemporaryDirectory(prefix="install-", dir=cache) as temporary:
        staging = Path(temporary)
        with zipfile.ZipFile(io.BytesIO(download(release["runtime"]))) as wheel:
            if sum(member.file_size for member in wheel.infolist()) > 32 * 1024 * 1024:
                raise RuntimeError(
                    "MicroVM runtime archive exceeds its extraction limit"
                )
            for member in wheel.infolist():
                target = staging / member.filename
                if not within(staging, target.resolve()) or stat.S_ISLNK(
                    member.external_attr >> 16
                ):
                    raise RuntimeError("unsafe path in the MicroVM runtime archive")
                if member.is_dir():
                    target.mkdir(parents=True, exist_ok=True)
                else:
                    target.parent.mkdir(parents=True, exist_ok=True)
                    target.write_bytes(wheel.read(member))
                    target.chmod((member.external_attr >> 16) & 0o777 or 0o600)
        with tarfile.open(
            fileobj=io.BytesIO(download(release["busybox"])), mode="r:gz"
        ) as package:
            member = package.getmember(release["busybox"]["member"])
            if not member.isfile() or member.size > MAX_DOWNLOAD_BYTES:
                raise RuntimeError("invalid static BusyBox archive member")
            busybox = staging / "busybox"
            busybox.write_bytes(package.extractfile(member).read())
            busybox.chmod(0o700)

        (staging / "installed.json").write_text(json.dumps(release), encoding="utf-8")
        if destination.exists():
            shutil.rmtree(destination)
        os.rename(staging, destination)
    return destination


def guard_parent(expected_parent):
    # Linux clears this flag on fork. Set it in each VM process before exec so
    # SIGKILL, SSH loss and service shutdown cannot leave a detached QEMU running.
    libc = ctypes.CDLL(None, use_errno=True)
    if libc.prctl(1, signal.SIGKILL, 0, 0, 0) != 0:
        raise OSError(ctypes.get_errno(), "cannot arm MicroVM parent-death signal")
    if os.getppid() != expected_parent:
        os._exit(125)


def virtme_arguments(runtime, kernel, invocation, request):
    vm_type, addresses = vm_options(request)
    if request.get("provision"):
        addresses = []
    if addresses:
        # Configuration data is never accepted as arbitrary QEMU options.
        if (
            not isinstance(addresses, list)
            or len(addresses) > 16
            or any(
                not isinstance(address, str)
                or not re.fullmatch(
                    r"[0-9a-f]{4}:[0-9a-f]{2}:[01][0-9a-f]\.[0-7]", address
                )
                for address in addresses
            )
        ):
            raise RuntimeError("invalid GPU PCI addresses")
    arguments = [
        "virtme-run",
        "--arch",
        "x86_64" if vm_type == "q35" else "microvm",
        "--qemu-bin",
        shutil.which("qemu-system-x86_64", path=SYSTEM_PATH),
        "--kimg",
        str(kernel),
        "--mods",
        "use",
        "--force-9p",
        "--force-initramfs",
        "--no-virtme-ng-init",
        "--show-boot-console",
        "--busybox",
        str(runtime / "busybox"),
        "--memory",
        "4096M",
        "--cpus",
        "2",
        "--rwdir",
        "/run/zentrale-workspace=" + str(invocation / "workspace"),
        "--rwdir",
        "/run/zentrale-temporary=" + str(invocation / "temporary"),
        "--rwdir",
        "/run/zentrale-control=" + str(invocation),
        "--script-sh",
        "/usr/bin/python3 -I /run/zentrale-control/guest.py",
    ]
    if kernel.parent.parent != Path("/"):
        arguments.extend(
            ["--rodir", "/run/zentrale-kernel=" + str(kernel.parent.parent)]
        )
    for index, entry in enumerate(request.get("storage") or []):
        arguments.extend(
            [
                "--rwdir",
                "/run/zentrale-storage-"
                + str(index)
                + "="
                + str(invocation / ("storage-" + str(index))),
            ]
        )
    if not request.get("provision") and not request.get("editorPort"):
        arguments.extend(["--net", "user"])
    arguments.extend(
        [
            "--qemu-opts",
            "-drive",
            "file="
            + str(invocation / "root.qcow2")
            + ",if=none,id=fedora,format="
            + request.get("diskFormat", "qcow2"),
            "-device",
            ("virtio-blk-pci" if vm_type == "q35" else "virtio-blk-device")
            + ",drive=fedora",
        ]
    )
    if request.get("editorPort"):
        # Only loopback is reachable on the SSH host. The guest relay also
        # authenticates every stream before exposing its private VS Code socket.
        arguments.extend(
            [
                "-netdev",
                "user,id=zentrale,hostfwd=tcp:127.0.0.1:"
                + str(request["editorPort"])
                + "-:9842",
                "-device",
                ("virtio-net-pci" if vm_type == "q35" else "virtio-net-device")
                + ",netdev=zentrale",
            ]
        )
    if vm_type == "q35":
        arguments.extend(
            ["-machine", "q35,accel=kvm", "-nodefaults", "-no-user-config"]
        )

    slots = {}
    for address in sorted(addresses):
        slot, function = address.rsplit(".", 1)
        if slot not in slots:
            port = "zentrale-gpu-" + str(len(slots))
            slots[slot] = port
            arguments.extend(
                ["-device", "pcie-root-port,id=" + port + ",chassis=" + str(len(slots))]
            )
        option = (
            "vfio-pci,host=" + address + ",bus=" + slots[slot] + ",addr=0x0." + function
        )
        if function == "0":
            option += ",multifunction=on"
        arguments.extend(["-device", option])
    return arguments


def execute_vm(runtime, kernel, invocation):
    request = json.loads((invocation / "request.json").read_text(encoding="utf-8"))
    sys.path.insert(0, str(runtime))
    os.environ["PATH"] = SYSTEM_PATH
    os.environ["HOME"] = str(private_directory(runtime.parent / "tool-home"))
    os.environ.pop("PYTHONPATH", None)

    # The wheel's QEMU path forks once and then execs. Keep the dependency
    # unmodified while ensuring that this child dies with its trusted runner.
    original_fork = os.fork

    def guarded_fork():
        parent = os.getpid()
        child = original_fork()
        if child == 0:
            guard_parent(parent)
        return child

    os.fork = guarded_fork
    from virtme import resources

    # Keep the pinned dependency unchanged and provide our own small PID 1.
    # A general-purpose host udev is neither needed nor started in this guest.
    resources.find_guest_tools = lambda: str(invocation)
    from virtme.commands.run import main as virtme_main

    sys.argv = virtme_arguments(runtime, kernel, invocation, request)
    return virtme_main()


def atomic_json(filename, value):
    temporary = filename.with_name(filename.name + "." + secrets.token_hex(8) + ".new")
    temporary.write_text(json.dumps(value), encoding="utf-8")
    os.replace(temporary, filename)


def process_reference(pid=None):
    pid = os.getpid() if pid is None else int(pid)
    fields = Path("/proc", str(pid), "stat").read_text().rsplit(") ", 1)[1].split()
    return {"pid": pid, "started": fields[19], "state": fields[0]}


def alive(reference):
    try:
        expected = json.loads(reference)
        if (
            not isinstance(expected, dict)
            or "pid" not in expected
            or "started" not in expected
        ):
            return False
        pid = expected["pid"]
        actual = process_reference(pid)
        return actual["state"] not in ("Z", "X") and (
            expected["started"] == actual["started"]
        )
    except (ValueError, ProcessLookupError, FileNotFoundError):
        return False


def install_fedora(cache, artifact):
    """Verify the official rootfs without unpacking or executing it on the host."""
    destination = cache / ("fedora-" + artifact["sha256"] + ".tar.gz")
    if destination.is_file():
        return destination
    temporary = destination.with_suffix(".download")
    digest = hashlib.sha256()
    size = 0
    try:
        with (
            urllib.request.urlopen(artifact["url"], timeout=60) as response,
            temporary.open("wb") as output,
        ):
            while block := response.read(1024 * 1024):
                size += len(block)
                if size > 128 * 1024 * 1024:
                    raise RuntimeError("Fedora rootfs exceeds its download limit")
                digest.update(block)
                output.write(block)
        if digest.hexdigest() != artifact["sha256"]:
            raise RuntimeError("Fedora rootfs failed SHA-256 verification")
        os.replace(temporary, destination)
    finally:
        temporary.unlink(missing_ok=True)
    return destination


def prepare_control(directory, configuration, runtime, request):
    private_directory(directory)
    atomic_json(directory / "request.json", request)
    (directory / "guest.py").write_text(configuration["guest"], encoding="utf-8")
    (directory / "streams.py").write_text(configuration["streams"], encoding="utf-8")
    (directory / "terminal.py").write_text(configuration["terminal"], encoding="utf-8")
    entry = directory / "virtme-init"
    entry.write_text(configuration["init"], encoding="utf-8")
    entry.chmod(0o700)
    os.link(runtime / "busybox", directory / "busybox")
    for name, source in [
        ("workspace", request["workspace"]),
        ("temporary", request["temporaryDirectory"]),
    ]:
        (directory / name).symlink_to(source, target_is_directory=True)
    for index, entry in enumerate(request.get("storage") or []):
        (directory / ("storage-" + str(index))).symlink_to(
            entry["hostPath"], target_is_directory=True
        )

    private_directory(directory / "requests")


def launch_vm(runtime, kernel, control):
    parent = os.getpid()
    child = os.fork()
    if child == 0:
        guard_parent(parent)
        os.setsid()
        try:
            status = execute_vm(runtime, kernel, control)
        except BaseException as error:
            print("MicroVM failed: " + str(error), file=sys.stderr, flush=True)
            status = 125
        os._exit(status)
    return child


def terminate_vm(child, control):
    (control / "stop").touch()
    deadline = time.monotonic() + 8
    while time.monotonic() < deadline:
        if os.waitpid(child, os.WNOHANG)[0]:
            return
        time.sleep(0.05)
    try:
        os.killpg(child, signal.SIGKILL)
    except ProcessLookupError:
        pass
    os.waitpid(child, 0)


def provision_base(cache, configuration, runtime, kernel):
    artifact = configuration["release"]["fedora"]
    archive = install_fedora(cache, artifact)
    base = cache / ("fedora-" + artifact["sha256"] + ".raw")
    if base.is_file():
        return base

    with tempfile.TemporaryDirectory(prefix="fedora-build-", dir=cache) as temporary:
        control = Path(temporary)
        scratch = control / "scratch"
        scratch.mkdir()
        request = {
            "workspace": str(scratch),
            "temporaryDirectory": str(scratch),
            "provision": True,
            "diskFormat": "raw",
            "vmType": configuration.get("vmType", "microvm"),
        }
        prepare_control(control, configuration, runtime, request)
        (control / "rootfs.tar.gz").symlink_to(archive)
        disk = control / "root.qcow2"
        subprocess.run(
            [
                shutil.which("qemu-img", path=SYSTEM_PATH),
                "create",
                "-q",
                "-f",
                "raw",
                str(disk),
                "8G",
            ],
            check=True,
        )

        with (control / "boot.log").open("w") as log:
            parent = os.getpid()
            child = os.fork()
            if child == 0:
                guard_parent(parent)
                os.dup2(log.fileno(), 1)
                os.dup2(log.fileno(), 2)
                try:
                    status = execute_vm(runtime, kernel, control) or 0
                except BaseException:
                    traceback.print_exc()
                    status = 125
                sys.stdout.flush()
                sys.stderr.flush()
                os._exit(status)
            deadline = time.monotonic() + 90
            try:
                while time.monotonic() < deadline:
                    finished, status = os.waitpid(child, os.WNOHANG)
                    if finished:
                        child = None
                        if (
                            os.waitstatus_to_exitcode(status) != 0
                            or not (control / "provisioned").exists()
                        ):
                            raise bootstrap_failure(
                                cache,
                                control,
                                "Fedora initialization failed",
                                os.waitstatus_to_exitcode(status),
                            )
                        os.rename(disk, base)
                        (cache / "fedora-bootstrap-failure.json").unlink(
                            missing_ok=True
                        )
                        return base
                    time.sleep(0.1)
                raise bootstrap_failure(
                    cache, control, "Fedora initialization timed out"
                )
            finally:
                if child is not None:
                    terminate_vm(child, control)


def bootstrap_failure(cache, control, message, exit_code=None):
    with (control / "boot.log").open("rb") as source:
        source.seek(0, os.SEEK_END)
        source.seek(max(0, source.tell() - 10000))
        logs = source.read().decode("utf-8", errors="replace").strip()
    diagnostic = {"error": message, "exit": exit_code, "log": logs}
    atomic_json(cache / "fedora-bootstrap-failure.json", diagnostic)
    status = " (exit " + str(exit_code) + ")" if exit_code is not None else ""
    return RuntimeError(
        message + status + "\n" + (logs or "No guest boot output was captured.")
    )


def ensure_ready(cache, configuration, kernel):
    with (cache / "setup.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if kernel is None:
            kernel = install_kernel(cache, configuration["release"]["kernel"])
            if configuration.get("vmType") == "q35":
                check_guest_kernel_modules(
                    kernel.parent.parent,
                    configuration["release"]["kernel"]["version"],
                    vm_type="q35",
                )
        runtime = install_runtime(cache, configuration["release"])
        base = provision_base(cache, configuration, runtime, kernel)
    return runtime, base, kernel


def scope_directory(cache, execution, allow_rebind=False):
    agent, project = execution.get("agentId", ""), execution.get("projectId", "")
    if not all(
        isinstance(value, str) and re.fullmatch(r"[A-Za-z0-9_.-]{1,80}", value)
        for value in (agent, project)
    ):
        raise RuntimeError("MicroVM requires an agent and project identity")
    identity = {
        "agentId": agent,
        "projectId": project,
        "workspace": execution["workspace"],
        "temporaryDirectory": execution["temporaryDirectory"],
    }

    scopes = private_directory(cache / "agents")
    key = hashlib.sha256((project + "\0" + agent).encode()).hexdigest()
    scope = private_directory(scopes / key)
    metadata = scope / "identity.json"
    with (scope / "identity.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if metadata.exists():
            previous = json.loads(metadata.read_text())
            if previous != identity and not allow_rebind:
                raise RuntimeError(
                    "MicroVM workspace changed; reset the environment before reassigning its directory"
                )
        if not metadata.exists():
            atomic_json(metadata, identity)
    return scope


def stop_session(scope):
    control = scope / "session"
    if not control.is_dir():
        return
    (control / "stop").touch()
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        try:
            pid = (control / "supervisor.pid").read_text()
        except FileNotFoundError:
            break
        if not alive(pid) or (control / "stopped").exists():
            break
        time.sleep(0.1)
    else:
        raise RuntimeError("MicroVM did not stop; its disk was retained")
    shutil.rmtree(control)


def supervise(runtime, kernel, control, gpu_leases=()):
    """Own QEMU independently of individual SSH tool sessions."""
    os.setsid()
    null = os.open("/dev/null", os.O_RDONLY)
    os.dup2(null, 0)
    os.close(null)
    with (control / "boot.log").open("w") as log:
        os.dup2(log.fileno(), 1)
        os.dup2(log.fileno(), 2)

    # A detached owner must not retain the staging SSH descriptors or its
    # parent's flock. Otherwise the next action would wait on our own lock.
    for entry in Path("/proc/self/fd").iterdir():
        descriptor = int(entry.name)
        if descriptor > 2 and descriptor not in gpu_leases:
            try:
                os.close(descriptor)
            except OSError:
                pass
    (control / "supervisor.pid").write_text(json.dumps(process_reference()))
    child = launch_vm(runtime, kernel, control)
    boot_deadline = time.monotonic() + 300
    try:
        while True:
            finished, status = os.waitpid(child, os.WNOHANG)
            if finished:
                child = None
                atomic_json(
                    control / "failure.json",
                    {
                        "error": "MicroVM stopped unexpectedly",
                        "exit": os.waitstatus_to_exitcode(status),
                    },
                )
                return
            for job in (control / "requests").iterdir():
                try:
                    if (job / "started").exists() and not alive(
                        (job / "client.pid").read_text()
                    ):
                        (job / "cancel").touch()
                        (job / "acknowledged").touch()
                    elif (
                        not (job / "started").exists()
                        and time.time() - job.stat().st_mtime > 3600
                    ):
                        shutil.rmtree(job)
                except FileNotFoundError:
                    pass  # PID 1 may have removed an acknowledged mailbox.
            if (control / "stop").exists():
                return
            if (
                not (control / "ready.json").exists()
                and time.monotonic() > boot_deadline
            ):
                atomic_json(
                    control / "failure.json",
                    {"error": "Fedora guest startup timed out"},
                )
                return
            time.sleep(0.1)
    finally:
        if child is not None:
            terminate_vm(child, control)
        for descriptor in gpu_leases:
            os.close(descriptor)
        (control / "stopped").touch()


def session_identity(configuration, execution):
    vm_type, addresses = vm_options(configuration)
    return hashlib.sha256(
        json.dumps(
            {
                "runner": configuration["runnerHash"],
                "vmType": vm_type,
                "gpuDevices": addresses,
                "storage": execution.get("storage") or [],
            },
            sort_keys=True,
        ).encode()
    ).hexdigest()


def guest_responding(control):
    try:
        return (control / "ready.json").is_file() and time.time() - (
            control / "heartbeat"
        ).stat().st_mtime <= 30
    except FileNotFoundError:
        return False


def active_invocations(control):
    if not (control / "requests").is_dir():
        return False
    for job in (control / "requests").iterdir():
        try:
            if (
                (job / "started").exists()
                and not (job / "status").exists()
                and not (job / "cancel").exists()
                and alive((job / "client.pid").read_text())
            ):
                return True
        except FileNotFoundError:
            pass
    return False


def ensure_session(scope, configuration, runtime, base, kernel, execution):
    control = scope / "session"
    vm_type, addresses = vm_options(configuration)
    identity = session_identity(configuration, execution)
    if control.is_dir():
        if (
            (control / "version").exists()
            and (control / "version").read_text() == identity
            and not (control / "stopped").exists()
            and (control / "supervisor.pid").exists()
            and alive((control / "supervisor.pid").read_text())
        ):
            if guest_responding(control):
                return control
        if active_invocations(control):
            raise RuntimeError(
                "Fedora guest needs recovery or an update; active jobs were preserved"
            )
        stop_session(scope)

    # Keep disk preparation and device reservations ahead of the detached owner.
    with socket.socket() as reservation:
        reservation.bind(("127.0.0.1", 0))
        editor_port = reservation.getsockname()[1]
    request = dict(
        execution,
        uid=os.getuid(),
        gid=os.getgid(),
        network=True,
        editorPort=editor_port,
        vmType=vm_type,
        gpuDevices=addresses,
    )
    prepare_control(control, configuration, runtime, request)
    (control / "version").write_text(identity)
    disk = scope / "root.qcow2"
    if not disk.exists():
        subprocess.run(
            [
                shutil.which("qemu-img", path=SYSTEM_PATH),
                "create",
                "-q",
                "-f",
                "qcow2",
                "-F",
                "raw",
                "-b",
                str(base),
                str(disk),
            ],
            check=True,
        )
    (control / "root.qcow2").symlink_to(disk)

    gpu_leases = []
    if addresses:
        vfio = vfio_helpers(configuration)
        devices = vfio["inspect_devices"](addresses)
        gpu_leases = vfio["reserve_groups"](
            scope.parent.parent,
            devices,
            {"agentId": execution["agentId"], "projectId": execution["projectId"]},
        )
    try:
        child = os.fork()
    except BaseException:
        for descriptor in gpu_leases:
            os.close(descriptor)
        raise
    if child == 0:
        # No parent-death guard here: this owner spans multiple SSH sessions.
        # Its own QEMU children are guarded against supervisor death.
        try:
            supervise(runtime, kernel, control, gpu_leases)
        finally:
            for descriptor in gpu_leases:
                try:
                    os.close(descriptor)
                except OSError:
                    pass
            os._exit(0)
    for descriptor in gpu_leases:
        os.close(descriptor)

    # Accept the new session only after the guest returns its boot proof.
    deadline = time.monotonic() + 300
    while time.monotonic() < deadline:
        if (control / "ready.json").exists():
            proof = json.loads((control / "ready.json").read_text())
            host_boot = Path("/proc/sys/kernel/random/boot_id").read_text().strip()
            if proof["bootId"] == host_boot or proof["os"] != "fedora":
                raise RuntimeError("MicroVM did not prove a separate Fedora guest")
            return control
        if (control / "failure.json").exists() or (control / "stopped").exists():
            break
        time.sleep(0.1)

    logs = (control / "boot.log").read_text()[-10000:]
    stop_session(scope)
    raise RuntimeError("Fedora MicroVM startup failed\n" + logs)


def stage(cache, execution, configuration, runtime, base, kernel):
    scope = scope_directory(cache, execution)
    with (scope / "lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        control = ensure_session(scope, configuration, runtime, base, kernel, execution)
        invocation = private_directory(control / "requests" / secrets.token_hex(16))
        request = dict(execution)
        command = request.pop("command")
        (invocation / "command.sh").write_text(command, encoding="utf-8")
        atomic_json(invocation / "request.json", request)
    return invocation


def run_invocation(invocation):
    control = invocation.parent.parent
    MailboxStream = runpy.run_path(str(control / "streams.py"))["MailboxStream"]
    request = json.loads((invocation / "request.json").read_text())
    cancelled = lambda: (
        (invocation / "cancel").exists() or (invocation / "status").exists()
    )
    (invocation / "client.pid").write_text(json.dumps(process_reference()))
    (invocation / "stdin").touch()

    def copy_input():
        if request.get("protocolIO"):
            stream = MailboxStream(invocation, "stdin", cancelled)
            try:
                while block := os.read(0, 65536):
                    stream.write(block)
            except OSError:
                try:
                    (invocation / "cancel").touch()
                except FileNotFoundError:
                    pass
            finally:
                stream.close()
            return
        with (invocation / "stdin").open("ab", buffering=0) as output:
            size = 0
            while block := os.read(0, 65536):
                size += len(block)
                if size > 16 * 1024 * 1024:
                    (invocation / "cancel").touch()
                    break
                output.write(block)
        (invocation / "stdin.eof").touch()

    threading.Thread(target=copy_input, daemon=True).start()
    (invocation / "started").touch()
    streams = {name: MailboxStream(invocation, name) for name in ("stdout", "stderr")}

    def write_all(descriptor, block):
        remaining = memoryview(block)
        while remaining:
            written = os.write(descriptor, remaining)
            if written <= 0:
                raise BrokenPipeError("MicroVM output channel closed")
            remaining = remaining[written:]

    def copy_output():
        for name, descriptor in [("stdout", 1), ("stderr", 2)]:
            # Bound each pass so a busy stdout cannot starve stderr or status.
            for _ in range(MailboxStream.QUEUED_CHUNKS):
                block = streams[name].read()
                if not block:
                    break
                write_all(descriptor, block)

    try:
        while True:
            copy_output()
            if (invocation / "status").exists():
                copy_output()
                # PID 1 can fail before it has created output streams.
                diagnostic = invocation / "stderr"
                if diagnostic.exists():
                    with diagnostic.open("rb") as source:
                        write_all(2, source.read(8192))
                return int((invocation / "status").read_text())
            if (control / "stopped").exists() or not alive(
                (control / "supervisor.pid").read_text()
            ):
                raise RuntimeError(
                    "the Fedora MicroVM connection was interrupted; its disk is retained"
                )
            time.sleep(0.03)
    finally:
        (invocation / "cancel").touch()
        # PID 1 removes completed mailboxes once the reader acknowledges them.
        (invocation / "acknowledged").touch()


def validate_execution(execution):
    if not all(
        Path(execution[key]).is_absolute()
        for key in ("workspace", "temporaryDirectory")
    ):
        raise RuntimeError("MicroVM workspace paths must be absolute")
    roots = [
        Path(execution[key]).resolve() for key in ("workspace", "temporaryDirectory")
    ]
    roots.extend(
        Path(entry["hostPath"]).resolve() for entry in storage_mounts(execution)
    )
    for root in roots:
        if str(root) == "/" or any(
            within(Path(directory), root)
            for directory in ("/proc", "/sys", "/dev", "/run")
        ):
            raise RuntimeError(
                "MicroVM workspaces cannot use kernel or control filesystems"
            )
    return cache_directory(roots)


def main():
    action = sys.argv[1]
    configuration = json.loads(sys.argv[2])
    guard_parent(os.getppid())
    kernel = (
        prerequisites(configuration)
        if action in ("select", "ensure", "stage", "prepare")
        else None
    )

    if action in ("select", "ensure"):
        ensure_ready(cache_directory(), configuration, kernel)
        print("microvm", flush=True)
        return 0

    if action in ("stage", "prepare", "reset", "remove", "stop", "info"):
        execution = json.load(sys.stdin)["execution"]
        if action in ("stage", "prepare"):
            storage_mounts(execution, configuration.get("storage") or [])
        cache = validate_execution(execution)
        runtime, base, kernel = (
            ensure_ready(cache, configuration, kernel)
            if action in ("stage", "prepare")
            else (None, None, None)
        )

        if action == "prepare":
            scope = scope_directory(cache, execution)
            with (scope / "lock").open("a") as lock:
                fcntl.flock(lock, fcntl.LOCK_EX)
                control = ensure_session(
                    scope, configuration, runtime, base, kernel, execution
                )
                proof = json.loads((control / "ready.json").read_text())
                proof["editorPort"] = json.loads(
                    (control / "request.json").read_text()
                )["editorPort"]
                print(json.dumps(proof), flush=True)
            return 0

        if action == "stage":
            print(
                stage(cache, execution, configuration, runtime, base, kernel),
                flush=True,
            )
            return 0

        scope = scope_directory(
            cache, execution, allow_rebind=action in ("reset", "remove", "stop")
        )
        with (scope / "lock").open("a") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            if action == "info":
                control = scope / "session"
                proof = (
                    json.loads((control / "ready.json").read_text())
                    if (control / "ready.json").exists()
                    else {}
                )
                print(
                    json.dumps(
                        dict(
                            proof,
                            persistent=True,
                            running=(control / "supervisor.pid").exists()
                            and alive((control / "supervisor.pid").read_text())
                            and not (control / "stopped").exists(),
                            agentId=execution["agentId"],
                            projectId=execution["projectId"],
                        )
                    )
                )
                return 0
            if action == "reset":
                requests = scope / "session" / "requests"
                if requests.is_dir() and any(
                    (job / "started").exists()
                    and not (job / "status").exists()
                    and alive((job / "client.pid").read_text())
                    for job in requests.iterdir()
                ):
                    raise RuntimeError("stop running jobs before resetting the MicroVM")

            stop_session(scope)
            if action in ("reset", "remove"):
                (scope / "root.qcow2").unlink(missing_ok=True)
            if action == "remove":
                (scope / "identity.json").unlink(missing_ok=True)
            elif action == "reset":
                atomic_json(
                    scope / "identity.json",
                    {
                        key: execution[key]
                        for key in (
                            "agentId",
                            "projectId",
                            "workspace",
                            "temporaryDirectory",
                        )
                    },
                )
            print(json.dumps({"action": action, "projectFilesPreserved": True}))
            return 0

    if action == "run":
        invocation = Path(sys.argv[3])
        cache = cache_directory()
        if (
            invocation != invocation.resolve()
            or not within(cache / "agents", invocation)
            or invocation.parent.name != "requests"
            or not re.fullmatch(r"[a-f0-9]{32}", invocation.name)
        ):
            raise RuntimeError("invalid MicroVM invocation path")
        private_directory(invocation)
        return run_invocation(invocation)
    raise RuntimeError("unknown managed MicroVM action")


if __name__ == "__main__":
    try:
        sys.exit(main())
    except UnavailableError as error:
        if len(sys.argv) < 2 or sys.argv[1] != "select":
            print("MicroVM unavailable: " + str(error), file=sys.stderr)
        sys.exit(UNAVAILABLE)
    except Exception as error:
        print("MicroVM setup failed: " + str(error), file=sys.stderr)
        sys.exit(1)
