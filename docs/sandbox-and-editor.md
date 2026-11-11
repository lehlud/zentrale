# Sandboxes, GPUs, and editor

[← Documentation](README.md)

## Fedora guests

Automatic sandbox selection prefers the integrated QEMU/KVM runner with Fedora
Minimal 45. Each main conversation has one persistent guest per selected host.
Its subagents and browser VS Code share packages, scratch files, and project
files. Independent GPU jobs can still use additional hosts concurrently.

Guests include Git, ripgrep, Python 3 and pip, curl, SSH clients, jq, less, Make,
fish, and standard file, archive, and process tools. Missing standard packages
are added without resetting an existing disk. Interactive guest terminals use
fish; harness commands use a POSIX shell.

The SSH host needs Linux x86_64, accessible `/dev/kvm`, QEMU with `microvm` or
`q35`, `qemu-img`, Python 3.10+, `mkfs.ext4`, `tar`, and `modprobe`. Preparing a
fallback kernel's modules also needs `depmod`. Automatic CPU selection can use
Bubblewrap with cgroups v2 and `systemd-run --user` when VM prerequisites are
absent. An explicit q35 or GPU assignment never falls back to weaker execution.
Download and boot failures are reported as errors.

A compatible readable host kernel is reused. Only when necessary does Zentrale
download a pinned guest kernel and modules, verify them, and cache them privately.
Host kernels, drivers, and packages are not modified. Artifact versions and
checksums live in [release.json](../backend/remote/microvm/release.json).

Guests use 2 vCPUs, 4 GiB RAM, and a sparse disk capped at 8 GiB. Packages and
guest files survive idle periods and stop/start. Project files and configured
storage are mounted separately. Failover does not move a guest disk to another
host. Deleting a main agent removes its descendants and their managed VMs and
disks. Removing a subagent preserves the main agent's shared guest.

## Permissions and lifecycle

Environmental actions receive independent model review. Technical isolation
still depends on the selected sandbox. Network permission is per action;
network-free guest jobs get their own namespace with loopback only, including
guest-root jobs.

If neither VM nor Bubblewrap is available, automatic selection can report no
technical sandbox. Model review remains the action gate in that mode; it cannot
provide host-level filesystem or network isolation.

- `packages_install` installs guest RPMs and requires network permission.
- `exec` with `guestRoot:true` grants administration only inside the managed guest.
- `microvm_info` reports VM state, boot identity, and detected guest GPUs.
- `microvm_reset` is available only to the main agent, after subagents and jobs
  stop. It removes guest files, packages, and processes; project files and extra
  storage remain.

Zentrale starts environments automatically when a conversation continues and
stops them when no agent, background job, preparation, or VS Code session needs
them. Stops release RAM and GPU devices while retaining the disk and packages.
Agents have no stop command, and the interface needs no manual VM controls.
Cleanup also retries after connection failures and server restarts. Obsolete
per-subagent guests are stopped without deleting their retained disks.

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

Choose `q35` to automatically discover prepared, non-boot GPUs and their
same-slot companion functions at VM startup. Explicit `gpu_devices` still takes
priority when only selected GPUs should be assigned. Pools can set the VM default,
but explicit PCI addresses belong to individual members. This fragment belongs inside a
[complete pool configuration](machines.md#import-a-pool):

```yaml
vm_type: q35
machines:
  - id: gpu-a
    name: GPU A
    host: gpu-a.example.org
    user: developer
    gpu_devices: ["0000:01:00.0", "0000:01:00.1"]
```

Addresses must match the actual hardware and include required companion
functions, such as GPU audio. Zentrale checks device class, IOMMU groups,
`vfio-pci` binding, complete group assignment, VFIO access, and at least 4 GiB of
lockable memory. Host administration must prepare these prerequisites; Zentrale
does not rebind drivers or change host device permissions.

Automatic discovery uses only GPUs already bound to `vfio-pci`; native host
drivers and the host boot GPU are never taken over. All selected companion
functions must also satisfy the existing IOMMU/VFIO checks. Missing safe GPUs or
unavailable groups produce a diagnostic, not a silent CPU-only fallback. Device
discovery does not acquire or bypass the VM lifetime leases. Stopping, inspecting
or removing a VM does not require successful rediscovery.

IOMMU groups remain reserved for the VM's lifetime. PCI passthrough also needs a
compatible guest driver. CUDA requires matching NVIDIA driver and user-space
packages; host drivers are not copied into the guest. The fallback kernel does
not include a proprietary NVIDIA driver. A GPU tag does not prove readiness.

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
editor port. Multiple workbenches share the conversation's editor. Closing the
last workbench releases its session after a short reconnect grace period;
active requests finish first. Service shutdown and SSH loss also close sessions.
Closing the editor does not reset the guest disk.

Cache paths can be changed through `ZENTRALE_VSCODE_CACHE` on the server and
`ZENTRALE_VSCODE_SERVER_CACHE` / `ZENTRALE_VSCODE_SERVER_DATA` on SSH hosts.
VM setup diagnostics are kept in the private runtime directory at
`/var/tmp/zentrale-microvm-<uid>`.

HTTP servers started in the editor can be opened through the authenticated
[HTTPS preview gateway](preview.md), including a frontend and API on different
guest ports. No additional public host or container ports are required.
