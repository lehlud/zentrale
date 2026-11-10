# Shared environments: measurements and lifecycle

The environment belongs to a main conversation. Subagents and VS Code share it;
additional hosts remain available for independent GPU jobs. This removes the
repeated guest OS and package footprint without changing job ownership or history.

## Measurements on 10 November 2026

The affected project ran on the configured workmachine, a Linux x86_64 host with
16 logical CPUs and 58 GiB RAM. Both platforms used KVM, 2 vCPUs, a 4 GiB memory
limit, and the same retained Fedora disk contents. Each temporary guest was
allowed to settle for five seconds, then sampled three times over five-second
intervals without tool jobs. PSS measures proportionally attributed resident
memory; configured guest RAM is a limit, not a measured resident allocation.

| Platform | QEMU PSS | CPU in three idle samples | Warm preparation |
| --- | ---: | --- | ---: |
| microvm | 500 MiB | 28.4%, 26.6%, 27.4% | 24.3 s |
| q35 | 438 MiB | 24.5%, 24.9%, 21.2% | 19.1 s |

CPU percentages use one logical CPU as 100%. The host was heavily loaded, and
these short samples do not establish an inherent performance advantage for
either platform. They do show that duplicating this full environment is not
negligible on either platform. A new guest's first package preparation took
roughly 59–80 seconds in separate probes.

Ten pre-existing QEMU guests together accounted for approximately 20.8 GiB PSS
and 15.8 CPU cores during another sample. Most had no outstanding jobs. That is
an abnormal retained-runtime finding, not a normal per-VM overhead estimate.
Private disk copies shared extents where supported, so their apparent allocated
size was not treated as additional physical disk usage.

QEMU's [microvm platform](https://www.qemu.org/docs/master/system/i386/microvm.html)
removes devices and firmware facilities such as PCI and ACPI. It still runs the
chosen guest OS. Ubuntu's [microvm documentation](https://ubuntu.com/server/docs/explanation/virtualisation/qemu-microvm/)
also distinguishes platform boot savings from the cost of a full OS and workload.
Our measured Fedora and runner footprint therefore matters more to this decision
than minimal-guest figures from other VMMs. q35 remains necessary for our PCI GPU
passthrough configuration.

## Automatic ownership and cleanup

- Main and subagent tool calls use the main agent's stable VM identity on each
  host. Their histories and jobs remain separate.
- Active agent turns, command preparation, background jobs, and editor clients
  keep the relevant environment alive. The last user releases it automatically.
- Closing the last editor client allows a short reconnect grace period. A second
  tab or an active transfer keeps the editor session alive.
- Continuing work starts the retained disk again; installed packages remain.
- Only the main agent can request a reviewed reset, after workers and jobs stop.
  Agents cannot stop the environment themselves.
- Removing a main agent removes its descendants and managed disks. Removing a
  subagent preserves the shared environment.
- Shutdown stops supervised guests. Startup and periodic cleanup retry unused
  guests, including obsolete per-subagent scopes, without deleting old disks.

## Repository timeout investigation

The reported repository-inspection timeouts occurred after the previously
deployed fixes were already present. Host Git metadata checks completed in about
10 ms. Fresh guests, a private copy of the affected stopped guest, and the real
Go SSH/JSON path all passed; guest commands took roughly 3–10 seconds under load.
The exact earlier hang was not reproduced, so no single root cause is established.

The runtime now checks a guest dispatcher heartbeat before reusing a cached
session. A stale guest without live jobs is restarted using the same disk; active
jobs prevent recovery from silently killing their work. Detached supervisors also
have a bounded initial boot window. These guards address stale runtime reuse,
while sharing and automatic cleanup reduce accumulated resource pressure.

All temporary database snapshots, copied guest disks, and diagnostic VM scopes
were removed. The affected original guest disk was not started or modified by
these probes.
