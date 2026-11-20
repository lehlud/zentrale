# Sandboxes, GPUs, and editor

[← Documentation](README.md)

## Fedora guests

Automatic sandbox selection requires the integrated QEMU/KVM runner with Fedora
Minimal 45. Each project has one guest per actual remote machine. Independent
conversations, subagents, browser terminals and VS Code share that guest. A
project selects exactly one standalone machine or one pool. Pool members can
have separate project VMs, including concurrent GPU jobs.

Guests include Python 3 (`python` and `python3`), pip, Git, ripgrep (`rg`), fd,
ast-grep (`sg`), cloc, tree, bat, jq, yq, gron, jc, DuckDB, sqlite3, csvkit,
Miller (`mlr`), qsv, sd, Perl, delta, direnv, mise, just, Podman, curl, SSH
clients, less, Make, fish and standard file/archive/process tools. Missing
standard tools are added without resetting an existing disk. Upstream binaries
are version-pinned and checksum-verified; downloads and installation happen
inside the guest. Interactive terminals use fish; harness commands use a POSIX
shell. Agents use rootless Podman, installing Docker in the guest only when
Podman cannot satisfy a concrete requirement.

The SSH host needs Linux x86_64, accessible `/dev/kvm`, QEMU with `microvm`,
`qemu-img`, Python 3.10+, `mkfs.ext4`, `tar`, and `modprobe`. Preparing a fallback
kernel's modules also needs `depmod`. Automatic selection fails if these
requirements are unavailable. It never silently switches to Bubblewrap or host
execution. Weaker levels remain explicit administrator choices.

A compatible readable host kernel is reused. Only when necessary does Zentrale
download a guest kernel and modules and cache them privately. Alpine package
URLs are resolved from the official Alpine 3.22 `APKINDEX.tar.gz`, whose RSA
signature is checked against the public key pinned in
[release.json](../backend/remote/microvm/release.json). Kernel and NVIDIA headers
must have the same package version. BusyBox and the private musl loader use the
same verified index. Each APK's control checksum, name, version, architecture,
size, and SHA-256 data hash are checked before extraction, following
[Alpine's APKv2 format](https://github.com/alpinelinux/apk-tools/blob/master/doc/apk-v2.5.scd).

The signed index is cached with its repository and trusted-key identity. Cached
installations remain reusable without network access. If an APK returns 404 or
410 after Alpine replaces it, setup refreshes the index and retries once with
the new matching kernel/header pair. Signature and checksum errors stop setup.
Runtime directories include the resolved artifact identity, so running VMs
keep their own runtime files through an update.

Guests use the logical CPUs available to the runner, up to QEMU's `microvm`
limit, with 4 GiB RAM and a sparse disk capped at 8 GiB. Git uses one checkout
worker per guest vCPU within the guest process limit. Disks, VM artifacts and
per-project scratch normally live in private directories under `/tmp` on the
remote machine. When `/tmp` is quota-limited tmpfs and `/var/tmp` is a separate
filesystem, the runner keeps guest disks under `/var/tmp/zentrale-microvm-disks-<uid>`.
It moves an existing disk when the guest next starts. Reboots and temporary-file
cleanup may remove temporary data. Zentrale creates a new guest when needed.
Project files and configured storage are mounted separately
and retained. Pool failover does not move a guest disk to another host.
Deleting a conversation preserves the project's shared VM. Removing the project
queues cleanup of its VMs on their recorded hosts.

## Permissions and lifecycle

Environmental actions receive independent model review. Technical isolation
still depends on the selected sandbox. Network permission is per action;
network-free guest jobs get their own namespace with loopback only, including
guest-root jobs.

In VM mode, agent commands cannot request execution outside the VM. Arbitrary
`guestRoot` execution and agent-triggered VM reset are unavailable. The reviewed
`packages_install` helper installs guest RPMs with network permission.
`microvm_info` reports VM state, boot identity and detected GPUs.

Only the authenticated user's VM terminal enables passwordless `sudo` inside
the Fedora guest. Agent processes retain `no_new_privs` and a `nosuid` root.
Terminal administration still excludes capabilities that could expose the
VM init's host tooling filesystem. Host administration is never provided.

The project's upper-right three-dot menu offers **Reset VM**. It resets all
recorded VMs for that project on their actual hosts, after active work and
terminals have stopped. Packages and guest-only files are discarded; mounted
project files and configured storage remain. Agents cannot invoke this action.

Environments start when needed and stop when no agent, background job,
preparation, terminal or editor needs them. Idle stops release RAM and GPUs
and retain the guest disks. Transport recovery and service
restart behavior are described in [Remote daemon](remote-daemon.md).

The dispatcher answers health probes independently of guest and host clock
agreement. A healthy, compatible environment can finish existing jobs before
adopting a changed runner implementation. If its dispatcher stops responding,
Zentrale accounts for affected jobs as interrupted, closes old editor/MCP
connections, and restarts the retained disk automatically. The interrupted
commands are not replayed; their agents receive recovery notices and can resume
from saved files. Preparation retries once and reports an error only if recovery
itself cannot restore the environment.

The platform choice does not eliminate the cost of a full Fedora guest. See the
[VM measurements and design decision](vm-lifecycle-analysis.md).

Administrators can supply a custom `microvm_command` runner using the contract
`runner --root PROJECT --temporary-directory AGENT_TMP --network=false -- /bin/sh -c COMMAND`.
It must implement VM isolation, mounts, networking, I/O, and cleanup itself.
Browser editor sessions require the integrated runner.

## Guest paths

The managed Fedora user's home is `/home/zentrale`; commands, the integrated
terminal and VS Code use `/home/zentrale/project` for the project. Guest scratch
and cache paths use `/tmp/zentrale`, independent of the SSH host's username and
project ID. These are mounts of the existing project/scratch, not new checkouts
or moves of host data. Old project/scratch paths remain compatibility mounts for
queued actions and existing Git SSH settings; they do not expose the host home.
File tools also accept `/home/zentrale/project/...` with the same project-boundary
and symlink checks. Other sources within the project remain intact.

## GPU passthrough

All managed guests use QEMU’s `microvm` machine. Add the `GPU` property and
leave `gpu_devices` empty for automatic GPU setup. Zentrale
first looks for GPUs already prepared for VFIO. Otherwise, an accessible NVIDIA
host driver can be shared with the guest through
[NVKVM](https://github.com/reindertpelsma/nvkvm-pv/tree/v0.2.5). This works without
host root, IOMMU groups, driver rebinding, or host package installation. CPU
machines do not download the GPU runtime.

```yaml
machines:
  - id: gpu-a
    name: GPU A
    host: gpu-a.example.org
    user: developer
    properties: [GPU]
```

This fragment belongs inside a [complete pool configuration](machines.md#import-a-pool).
The same selection applies to agent commands, subagents, background jobs, and
browser VS Code in the project's shared guest on that host. GPU jobs can still be
scheduled across multiple pool members.

CPU guests keep the minimal virtio-MMIO machine. GPU guests enable its PCIe
bridge and ACPI for the forwarded device. There is no VM type setting in the
API or pool YAML; `vm_type` is rejected at both pool and member level.

### Rootless NVIDIA

The host needs an existing working NVIDIA driver, readable matching userspace
libraries, access to `/dev/nvidiactl`, `/dev/nvidia0`, `/dev/nvidia-uvm`, and its
normal KVM prerequisites. Building the small guest module also needs GCC, Make,
and binutils already on the host. The pinned QEMU binary needs glibc 2.38+ and
libslirp 4.7+. Zentrale verifies the pinned runtime and uses the signed Alpine
index to verify matching guest headers and private build tools. It builds
without privileges and caches the result outside guest-writable exports. A
compatible host kernel with headers is reused. The managed guest kernel is
needed when matching headers or required guest features are unavailable.

Only the forwarding module and a read-only copy of the matching NVIDIA libraries
are attached to Fedora. CUDA, OpenCL, Vulkan, OpenGL/EGL, NVENC, and NVDEC use the
host driver through virtio. Applications still need their normal guest packages,
such as PyTorch, an OpenCL/Vulkan loader, GLVND, or FFmpeg. No proprietary NVIDIA
kernel driver needs to be installed in Fedora. Readiness checks initialize CUDA
and require at least one device; missing GPU support fails rather than silently
starting a CPU-only environment. Driver changes require a fresh session after
existing work has ended.

This is shared driver forwarding, not an exclusive GPU or a hardened GPU tenant
boundary. NVKVM's syscall filter stays enabled; hosts with user namespaces use
its stronger namespace isolation. On the CIPs, which disable user namespaces,
its automatic mode uses seccomp without separate host UIDs or filesystem
namespaces for GPU helpers. GPU memory and faults remain shared host resources;
the VM's RAM limit does not cap all host GPU/pinned-memory allocations. Use this
mode for trusted workloads under the SSH account's existing GPU permissions.

Integration tested on a FAU CIP RTX 2080 with NVIDIA 615.71.10: 4,096-element
CUDA, OpenCL, and Vulkan kernels, OpenGL 4.6 and OpenGL ES 3.2 shaders,
and H.264 NVENC/NVDEC at 1920×1080, 30 frames. Hardware-decoded output matched
93,312,000 reference bytes exactly. Chromium WebGL verified rendered
pixels and WebGPU computed and verified 4,096 values on the NVIDIA adapter with software fallback disabled.

### Explicit VFIO

Set `gpu_devices` on an individual pool member to keep exclusive PCI passthrough:

```yaml
properties: [GPU]
gpu_devices: ["0000:01:00.0", "0000:01:00.1"]
```

Addresses must include required companion functions and complete IOMMU groups.
Zentrale checks non-boot device class, `vfio-pci` binding, group assignment,
VFIO access, and at least 4 GiB of lockable memory. An administrator must prepare
these prerequisites. Zentrale never rebinds host drivers or changes host device
permissions. Groups stay reserved for the VM's lifetime, and explicit addresses
never fall back to driver forwarding. The guest needs compatible GPU drivers.
Stopping or removing a VM does not require successful GPU rediscovery.

## Browser VS Code

**Open VS Code in a new tab** opens a full workbench in the agent's workspace.
On managed VMs, editor terminals, debugger, extensions, and agent commands share
the guest. Without a VM, the remote editor agent runs over SSH on the host.
Opening the editor on a GPU machine requires an owning conversation.

Zentrale serves the web assets. Both workbench and remote agent are pinned in
[the editor release file](../backend/vscode/release.json), with archive checksums.
Initial installation needs Microsoft's download servers. The editor distribution
supports Linux and macOS on x64 and ARM64; managed VMs have separate Linux host
requirements.

Sessions require authentication and use private SSH tunnels without a public
editor port. Multiple workbenches share the project's editor on that host. Closing the
last workbench releases its session after a short reconnect grace period;
active requests finish first. SSH loss and a Zentrale restart retain the remote
server and restore its existing workbench URL within the daemon lease.
Closing the editor does not reset the guest disk.

Cache paths can be changed through `ZENTRALE_VSCODE_CACHE` on the server and
`ZENTRALE_VSCODE_SERVER_CACHE` / `ZENTRALE_VSCODE_SERVER_DATA` on SSH hosts.
VM setup diagnostics are kept in the private runtime directory at
`/tmp/zentrale-microvm-<uid>`.

HTTP servers started in the editor can be opened through the authenticated
[HTTPS preview gateway](preview.md), including a frontend and API on different
guest ports. No additional public host or container ports are required.

### Terminal and editor startup

Opening the integrated fullscreen terminal or VS Code reuses a prepared native
VM only after checking the complete readiness fingerprint and its live boot
generation. Changes to sources, credentials, trust, storage, configuration or
the VM invalidate that proof and run preparation again. Explicit connection
checks still perform their full checks. GPU guests retain the existing
`microvm` PCIe/ACPI path; startup optimization does not change their VM type or
isolation.

The `workspace environment preparation` server log separates selection,
connection, readiness validation and cold preparation phases (`phases_ms`),
with `warm` and total `duration_ms`. It stops measuring before an interactive
session starts: keeping a terminal open for hours is not a slow startup.
These measurements include more than kernel boot; first-time downloads,
package installation, source preparation and VS Code installation have
different costs. Minimal-kernel boot benchmarks are not Fedora readiness
guarantees.

The subsequent `terminal startup` entry separates `lock_wait_ms` from
`pty_start_ms` (through the PTY READY acknowledgement), with `ready` and `error`
booleans. Neither timing entry contains terminal input or credentials. Slow SSH
availability/workspace receipt checks on an existing agent connection run
outside its state mutex, so another check cannot serialize a validated warm
editor/terminal opener behind its network wait.

While the fullscreen terminal covers a conversation, its large conversation
snapshot subscription is suspended. Other projects, connection status and
terminal traffic continue normally. Closing the terminal restores the
subscription once and receives the current server-pushed snapshot, without
HTTP polling or periodic WebSocket queries. The last chat snapshot and
scrollback stay in the UI during suspension.
