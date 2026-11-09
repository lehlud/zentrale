"""Read-only VFIO preflight and lifetime reservations; never rebind host drivers."""

import errno
import fcntl
import json
import os
from pathlib import Path
import re
import resource
import stat
import struct


PCI_ADDRESS = re.compile(r"[0-9a-f]{4}:[0-9a-f]{2}:[01][0-9a-f]\.[0-7]")
VFIO_GET_API_VERSION = 0x3B64
VFIO_CHECK_EXTENSION = 0x3B65
VFIO_GROUP_GET_STATUS = 0x3B67
VFIO_GROUP_FLAGS_VIABLE = 1
VFIO_GROUP_FLAGS_CONTAINER_SET = 2


def validate_addresses(addresses):
    if not isinstance(addresses, list) or len(addresses) > 16:
        raise RuntimeError("gpu_devices must be a list of at most 16 PCI addresses")
    if any(
        not isinstance(address, str) or not PCI_ADDRESS.fullmatch(address)
        for address in addresses
    ):
        raise RuntimeError("gpu_devices requires PCI addresses such as 0000:01:00.0")
    if len(set(addresses)) != len(addresses):
        raise RuntimeError("gpu_devices contains duplicate PCI addresses")
    return addresses


def group_status(group, device_root=Path("/dev/vfio")):
    if not re.fullmatch(r"[0-9]+", group):
        raise RuntimeError("invalid VFIO IOMMU group")
    try:
        descriptor = os.open(device_root / group, os.O_RDWR | os.O_CLOEXEC)
        try:
            status = bytearray(struct.pack("II", 8, 0))
            fcntl.ioctl(descriptor, VFIO_GROUP_GET_STATUS, status, True)
            return struct.unpack("II", status)[1]
        finally:
            os.close(descriptor)
    except OSError as error:
        if error.errno == errno.EBUSY:
            raise RuntimeError(
                "VFIO group " + group + " is already open in a running VM"
            ) from error
        raise RuntimeError(
            "VFIO group " + group + " is not accessible to the SSH user: " + str(error)
        ) from error


def inspect_devices(
    addresses,
    pci_root=Path("/sys/bus/pci/devices"),
    device_root=Path("/dev/vfio"),
    probe_groups=True,
):
    addresses = validate_addresses(addresses)
    if not addresses:
        return []

    devices = []
    gpu_slots = set()
    for address in addresses:
        device = pci_root / address
        try:
            device_class = int((device / "class").read_text().strip(), 16)
        except (OSError, ValueError) as error:
            raise RuntimeError(
                "configured GPU PCI function is unavailable: " + address
            ) from error
        is_gpu = device_class >> 16 == 0x03
        if is_gpu:
            gpu_slots.add(address.rsplit(".", 1)[0])

        group = device / "iommu_group"
        if not group.exists() or not re.fullmatch(r"[0-9]+", group.resolve().name):
            raise RuntimeError(
                "GPU PCI function "
                + address
                + " has no IOMMU group; an administrator must prepare IOMMU/VFIO on the host"
            )
        driver = device / "driver"
        driver_name = driver.resolve().name if driver.exists() else "none"
        if driver_name != "vfio-pci":
            raise RuntimeError(
                "GPU PCI function "
                + address
                + " is bound to "
                + driver_name
                + "; an administrator must assign it to vfio-pci"
            )
        devices.append(
            {"address": address, "group": group.resolve().name, "gpu": is_gpu}
        )

    if not gpu_slots:
        raise RuntimeError("gpu_devices must include a display/3D controller")
    if any(device["address"].rsplit(".", 1)[0] not in gpu_slots for device in devices):
        raise RuntimeError(
            "gpu_devices may contain only GPUs and their same-slot companion functions"
        )

    configured = set(addresses)
    for group in sorted({device["group"] for device in devices}):
        representative = next(device for device in devices if device["group"] == group)
        members = (
            pci_root / representative["address"] / "iommu_group" / "devices"
        ).iterdir()
        missing = []
        for member in members:
            # PCI bridges cannot be assigned by vfio-pci. The kernel's viable
            # group check remains authoritative for the complete topology.
            device_class = int((member / "class").read_text().strip(), 16)
            if device_class >> 8 != 0x0604 and member.name not in configured:
                missing.append(member.name)
        if missing:
            raise RuntimeError(
                "VFIO group "
                + group
                + " requires all companion PCI functions in gpu_devices: "
                + ", ".join(sorted(missing))
            )
        if not os.access(device_root / group, os.R_OK | os.W_OK):
            raise RuntimeError(
                "VFIO group " + group + " is not accessible to the SSH user"
            )
        # Opening a VFIO group is exclusive, including for query-only ioctls.
        # General inspection must also work while our own QEMU holds the group;
        # only a new VM probes it before acquiring its lifetime reservation.
        if (
            probe_groups
            and not group_status(group, device_root) & VFIO_GROUP_FLAGS_VIABLE
        ):
            raise RuntimeError(
                "VFIO group "
                + group
                + " is not viable; an administrator must prepare the complete IOMMU group"
            )

    # Probe the container API only after every configured group is valid.
    try:
        descriptor = os.open(device_root / "vfio", os.O_RDWR | os.O_CLOEXEC)
        try:
            if (
                fcntl.ioctl(descriptor, VFIO_GET_API_VERSION, 0) != 0
                or fcntl.ioctl(descriptor, VFIO_CHECK_EXTENSION, 1) != 1
            ):
                raise RuntimeError("the host does not expose VFIO type1 IOMMU support")
        finally:
            os.close(descriptor)
    except OSError as error:
        raise RuntimeError(
            "VFIO is unavailable to the SSH user: " + str(error)
        ) from error

    limit = resource.getrlimit(resource.RLIMIT_MEMLOCK)[0]
    if limit != resource.RLIM_INFINITY and limit < 4 * 1024 * 1024 * 1024:
        raise RuntimeError(
            "GPU passthrough requires a locked-memory limit of at least 4 GiB for the SSH user; an administrator must raise memlock"
        )
    return devices


def reserve_groups(cache, devices, owner):
    """Return locked fds to keep in the detached VM supervisor until shutdown."""
    if not devices:
        return []

    directory = cache / "gpu-leases"
    directory.mkdir(mode=0o700, exist_ok=True)
    metadata = directory.lstat()
    if (
        not stat.S_ISDIR(metadata.st_mode)
        or metadata.st_uid != os.getuid()
        or metadata.st_mode & 0o077
    ):
        raise RuntimeError("GPU reservations require a private cache directory")

    descriptors = []
    try:
        for group in sorted({device["group"] for device in devices}):
            if not re.fullmatch(r"[0-9]+", group):
                raise RuntimeError("invalid VFIO reservation group")
            descriptor = os.open(
                directory / (group + ".lock"),
                os.O_RDWR | os.O_CREAT | os.O_CLOEXEC | os.O_NOFOLLOW,
                0o600,
            )
            descriptors.append(descriptor)
            metadata = os.fstat(descriptor)
            if (
                not stat.S_ISREG(metadata.st_mode)
                or metadata.st_uid != os.getuid()
                or metadata.st_mode & 0o077
            ):
                raise RuntimeError("unsafe GPU reservation file")
            try:
                fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError as error:
                raise RuntimeError(
                    "GPU VFIO group "
                    + group
                    + " is reserved by another agent VM; stop that VM before reassigning the GPU"
                ) from error
            # Also detect a VM outside Zentrale or owned by another SSH user.
            if group_status(group) & VFIO_GROUP_FLAGS_CONTAINER_SET:
                raise RuntimeError(
                    "GPU VFIO group " + group + " is already assigned to a running VM"
                )
            os.ftruncate(descriptor, 0)
            os.write(descriptor, json.dumps(owner).encode())
        return descriptors
    except BaseException:
        for descriptor in descriptors:
            os.close(descriptor)
        raise
