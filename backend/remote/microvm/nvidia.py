"""Rootless NVIDIA driver forwarding for managed MicroVM guests.

Only the trusted host runner calls the provisioning functions. The guest gets
one read-only export containing its module and version-matched userspace, never
the build directory, QEMU executable, host credentials, or a host device mount.
"""

import ctypes
import hashlib
import io
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path

REQUIRED_LIBRARIES = (
    "libcuda",
    "libnvidia-ml",
    "libnvidia-ptxjitcompiler",
    "libnvidia-nvvm",
)
OPTIONAL_LIBRARIES = (
    "libnvidia-allocator",
    "libnvidia-encode",
    "libnvcuvid",
    "libnvidia-opencl",
    "libnvidia-glcore",
    "libnvidia-eglcore",
    "libnvidia-glsi",
    "libnvidia-tls",
    "libnvidia-rtcore",
    "libnvidia-gpucomp",
    "libnvidia-glvkspirv",
    "libnvidia-cfg",
    "libEGL_nvidia",
    "libGLX_nvidia",
    "libGLESv2_nvidia",
    "libGLESv1_CM_nvidia",
)
EXPORT = "/run/zentrale-nvidia"


def discover(
    pci_root=Path("/sys/bus/pci/devices"),
    devices=Path("/dev"),
    version_file=Path("/sys/module/nvidia/version"),
):
    """Read inventory; never detach the host's driver or load a host module."""
    addresses = []
    for device in sorted(pci_root.iterdir()):
        if not re.fullmatch(
            r"[0-9a-f]{4}:[0-9a-f]{2}:[01][0-9a-f]\.[0-7]", device.name
        ):
            continue
        if (
            (device / "vendor").read_text().strip() == "0x10de"
            and int((device / "class").read_text().strip(), 16) >> 16 == 3
            and (device / "driver").resolve().name == "nvidia"
        ):
            addresses.append(device.name)
    if not addresses:
        raise RuntimeError("no host NVIDIA GPU available for rootless forwarding")
    for name in ("nvidiactl", "nvidia0", "nvidia-uvm"):
        descriptor = os.open(devices / name, os.O_RDWR | os.O_CLOEXEC)
        os.close(descriptor)
    version = version_file.read_text().strip()
    if not re.fullmatch(r"[0-9]{3}\.[0-9]{1,3}(?:\.[0-9]{1,3})?", version):
        raise RuntimeError("cannot determine the host NVIDIA driver version")
    return {"backend": "nvidia", "devices": addresses, "driver": version}


def extract_regular(data, destination, prefix=""):
    """Extract only bounded regular files. Archive links cannot redirect writes."""
    total = 0
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as archive:
        for index, member in enumerate(archive):
            path = Path(member.name)
            if index >= 30000 or path.is_absolute() or ".." in path.parts:
                raise RuntimeError("unsafe NVIDIA runtime archive")
            if prefix and not member.name.startswith(prefix):
                continue
            if member.isdir():
                (destination / path).mkdir(parents=True, exist_ok=True)
            elif member.isfile():
                total += member.size
                if total > 1024 * 1024 * 1024:
                    raise RuntimeError(
                        "NVIDIA runtime archive exceeds its extraction limit"
                    )
                target = destination / path
                target.parent.mkdir(parents=True, exist_ok=True)
                with archive.extractfile(member) as source, target.open("xb") as output:
                    shutil.copyfileobj(source, output)
                target.chmod(0o700 if member.mode & 0o111 else 0o600)
            # Package-manager symlinks and device entries are never installed.


def libraries(version):
    result = subprocess.run(
        ["/sbin/ldconfig", "-p"], check=True, capture_output=True, text=True, timeout=15
    )
    candidates = {}
    for line in result.stdout.splitlines():
        if "=>" not in line or "x86-64" not in line:
            continue
        soname = line.strip().split()[0]
        source = Path(line.split("=>", 1)[1].strip()).resolve()
        for name in REQUIRED_LIBRARIES + OPTIONAL_LIBRARIES:
            if (
                soname.startswith(name + ".so")
                and source.name == name + ".so." + version
            ):
                candidates[soname] = source
                candidates[source.name] = source
                candidates[name + ".so"] = source
    missing = [name for name in REQUIRED_LIBRARIES if name + ".so" not in candidates]
    if missing:
        raise RuntimeError("host NVIDIA userspace is incomplete: " + ", ".join(missing))
    return candidates


def host_headers(kernel):
    if kernel is None or kernel.parent.parent != Path("/"):
        return None
    headers = Path("/lib/modules") / os.uname().release / "build"
    if not (headers / "Makefile").is_file():
        return None
    for module in ("drm", "drm_kms_helper", "drm_shmem_helper"):
        probe = subprocess.run(
            [
                "/usr/sbin/modprobe",
                "--config",
                "/dev/null",
                "--dry-run",
                "--show-depends",
                "--",
                module,
            ],
            check=False,
            capture_output=True,
            timeout=5,
        )
        if probe.returncode:
            return None
    return headers


def prepare(cache, release, kernel, inventory, download, install_kernel):
    """Publish a complete, private runtime atomically under the runner setup lock."""
    artifact = release["nvidia"]
    headers = host_headers(kernel)
    if headers is None:
        if artifact["headers"]["version"] != release["kernel"]["version"]:
            raise RuntimeError("NVIDIA guest headers do not match the fallback kernel")
        kernel = install_kernel(cache, release["kernel"])
        headers = None

    sources = libraries(inventory["driver"])
    identity = {
        "runtime": artifact,
        "kernel": str(kernel),
        "driver": inventory,
        "libraries": {
            str(p): [p.stat().st_size, p.stat().st_mtime_ns]
            for p in set(sources.values())
        },
        "layout": 2,
    }
    digest = hashlib.sha256(json.dumps(identity, sort_keys=True).encode()).hexdigest()[
        :24
    ]
    destination = cache / ("nvidia-" + digest)
    marker = destination / "installed.json"
    if marker.is_file() and json.loads(marker.read_text()) == identity:
        return kernel, str(destination)

    for tool in ("make", "gcc", "ld"):
        if shutil.which(tool) is None:
            raise RuntimeError(
                "rootless GPU setup requires the host build tool " + tool
            )
    print(
        "Preparing rootless NVIDIA guest support (host driver "
        + inventory["driver"]
        + ")",
        file=sys.stderr,
        flush=True,
    )
    with tempfile.TemporaryDirectory(prefix="nvidia-build-", dir=cache) as directory:
        staging = Path(directory)
        extract_regular(download(artifact, limit=100 * 1024 * 1024), staging)
        upstream = staging / artifact["directory"]
        options = []
        if headers is None:
            extract_regular(
                download(artifact["headers"], limit=32 * 1024 * 1024),
                staging / "headers",
                "usr/src/",
            )
            headers = (
                staging
                / "headers/usr/src"
                / ("linux-headers-" + release["kernel"]["version"])
            )
            extract_regular(download(artifact["musl"]), staging / "musl", "lib/")
            loader = staging / "musl/lib/ld-musl-x86_64.so.1"
            # Alpine's tiny host tools use musl. Run them with their private
            # loader; never install a loader or Alpine libraries on the host.
            for name in ("scripts/mod/modpost", "scripts/basic/fixdep"):
                binary = headers / name
                original = binary.with_name(binary.name + ".musl")
                binary.rename(original)
                binary.write_text(
                    "#!/bin/sh\nexec " + str(loader) + " " + str(original) + ' "$@"\n'
                )
                binary.chmod(0o700)
            # Distro GCC plugins are host-compiler plugins, not a module ABI.
            # Their Alpine libc cannot be loaded into a glibc host compiler.
            options = ["GCC_PLUGINS_CFLAGS=", "CONFIG_OBJTOOL="]
        command = [
            "make",
            "-C",
            str(upstream / "src/guest"),
            "KDIR=" + str(headers),
            "NVKVM_GRAPHICS=1",
            "-j2",
        ] + options
        built = subprocess.run(
            command, check=False, capture_output=True, text=True, timeout=240
        )
        if built.returncode:
            raise RuntimeError(
                "NVIDIA guest module build failed:\n"
                + (built.stdout + built.stderr)[-6000:]
            )

        export = staging / "guest"
        export.mkdir()
        shutil.copy2(upstream / "src/guest/nvkvm-guest.ko", export / "nvkvm-guest.ko")
        libs = export / "lib"
        libs.mkdir()
        for name, source in sources.items():
            if name == source.name:
                shutil.copyfile(source, libs / name)
            else:
                (libs / name).symlink_to(source.name)
        smi = shutil.which("nvidia-smi")
        if smi:
            shutil.copy2(smi, export / "nvidia-smi")
        (export / "inventory.json").write_text(json.dumps(inventory))
        # Discard build inputs. Only the immutable guest bundle is exported.
        published = staging / "published"
        published.mkdir()
        shutil.move(str(export), published / "guest")
        shutil.move(str(upstream / "qemu-nvkvm"), published / "qemu")
        for source in [
            *upstream.glob("*LICENSE*"),
            upstream / "NOTICE",
            upstream / "CREDITS",
        ]:
            if source.is_file():
                shutil.copy2(source, published / source.name)
        (published / "installed.json").write_text(json.dumps(identity))
        if destination.exists():
            shutil.rmtree(destination)
        os.rename(published, destination)
    return kernel, str(destination)


def initialize_guest(root, request, load_module, mount, bind_flags):
    """Load only the guest module, then attach GPU nodes and read-only libraries."""
    if request.get("gpuBackend") != "nvidia":
        return
    for name in ("virtio_pci", "drm", "drm_kms_helper", "drm_shmem_helper"):
        load_module(name)
    module = Path(EXPORT) / "nvkvm-guest.ko"
    descriptor = os.open(module, os.O_RDONLY | os.O_CLOEXEC)
    try:
        if ctypes.CDLL(None, use_errno=True).syscall(313, descriptor, b"", 0) != 0:
            raise OSError(ctypes.get_errno(), "load NVIDIA guest forwarding module")
    finally:
        os.close(descriptor)

    nodes = list(Path("/dev").glob("nvidia*")) + list(Path("/dev/dri").glob("renderD*"))
    if not Path("/dev/nvidia0").exists():
        raise RuntimeError("the NVIDIA forwarding guest did not create a GPU device")
    for source in nodes:
        if not source.is_char_device():
            continue
        os.chmod(source, 0o666)
        target = root / str(source).lstrip("/")
        target.parent.mkdir(parents=True, exist_ok=True)
        target.touch(exist_ok=True)
        mount(str(source), str(target), flags=bind_flags)

    target = root / "usr/local/lib/zentrale-nvidia"
    target.mkdir(parents=True, exist_ok=True)
    mount(EXPORT + "/lib", str(target), flags=bind_flags)
    config = root / "etc/ld.so.conf.d/00-zentrale-nvidia.conf"
    config.parent.mkdir(parents=True, exist_ok=True)
    config.write_text("/usr/local/lib/zentrale-nvidia\n")
    subprocess.run(
        ["/usr/sbin/chroot", str(root), "/sbin/ldconfig"], check=True, timeout=30
    )
    if Path(EXPORT, "nvidia-smi").is_file():
        target = root / "usr/local/bin/nvidia-smi"
        target.parent.mkdir(parents=True, exist_ok=True)
        target.touch(exist_ok=True)
        mount(EXPORT + "/nvidia-smi", str(target), flags=bind_flags)
    manifests = (
        ("etc/OpenCL/vendors/zentrale-nvidia.icd", "libnvidia-opencl.so.1\n"),
        (
            "usr/share/glvnd/egl_vendor.d/10_zentrale_nvidia.json",
            json.dumps(
                {
                    "file_format_version": "1.0.0",
                    "ICD": {"library_path": "libEGL_nvidia.so.0"},
                }
            ),
        ),
        (
            "usr/share/vulkan/icd.d/zentrale_nvidia.json",
            json.dumps(
                {
                    "file_format_version": "1.0.0",
                    "ICD": {
                        "library_path": "libGLX_nvidia.so.0",
                        "api_version": "1.3.0",
                    },
                }
            ),
        ),
    )
    for relative, content in manifests:
        target = root / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)


def verify_guest(root):
    """Readiness requires a real driver context, not merely a visible PCI device."""
    script = """import ctypes, json, sys
driver = ctypes.CDLL('libcuda.so.1')
status = driver.cuInit(0)
count = ctypes.c_int()
if status == 0:
    status = driver.cuDeviceGetCount(ctypes.byref(count))
print(json.dumps({'status': status, 'devices': count.value}), flush=True)
sys.exit(0 if status == 0 and count.value > 0 else 1)
"""
    result = subprocess.run(
        ["/usr/sbin/chroot", str(root), "/usr/bin/python3", "-I", "-c", script],
        check=False,
        capture_output=True,
        text=True,
        timeout=30,
    )
    if result.returncode:
        raise RuntimeError(
            "NVIDIA guest readiness failed: " + (result.stdout + result.stderr)[-2000:]
        )
    return json.loads(Path(EXPORT, "inventory.json").read_text())
