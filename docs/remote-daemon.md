# Remote daemon and reconnecting work

[← Documentation](README.md)

One remote Python daemon belongs to one Zentrale installation on each physical
host. A persistent installation ID and encrypted daemon secret live in the
database. Back up the database together with its adjacent credentials key;
restoring only one loses authenticated access to existing work. Separate
Zentrale installations have separate daemons and VM namespaces. Use one SSH
account per installation and physical host, including aliases and pool entries.

SSH bootstraps a small authenticated bridge to a private Unix socket under
`/tmp/zentrale-daemon-<installation hash>`. A filesystem lock prevents duplicate
daemons. When `/var/tmp` is on a separate filesystem, the daemon stores admission
receipts and output in a private directory there. Existing daemons adopt this
layout at their next start. Commands, process IO, VS Code connections and VM
terminals use that daemon. Connections share transports, while callers hold
independent references.
Closing a connection releases its reference; it does not terminate remote work.
Trust checks still apply on reconnect. Changing the configured SSH route never
migrates an existing process to a different machine.

## Transport and service restarts

The daemon remains alive for 15 minutes without authenticated contact from its
Zentrale. During that interval, processes and upstream sockets survive SSH loss
and a restart of the host running Zentrale. When Zentrale returns with its
original database and credentials, running jobs and editors attach to their
original resource IDs. Browser terminals reconnect to the same shell and replay
only unacknowledged input. Agent turns resume using their persisted histories.
Reconnects happen in the transport layer and need no model request.

Every resource has an admission ID, daemon generation and command fingerprint.
The daemon journals admission before executing a side effect. A repeated create
with the same identity retrieves its existing status. Input uses byte offsets
and content receipts; repeated identical input is acknowledged without writing
it twice. Conflicting input is rejected. Outputs use byte cursors. A retained
VS Code upstream socket is never replaced by silently replaying an HTTP request.

An absent resource, expired lease or changed daemon generation produces an
explicit error. Zentrale never launches the command again just to conceal that
loss. Requested job stops are persisted before cancellation and retried against
the original resource after service restart, including when a host is offline.
Actual remote host restart or daemon failure can lose running work; retained
project files/checkpoints remain the recovery boundary.

The daemon bounds output to 64 MiB per resource and 256 MiB overall; exceeding
the limit stops the resource with an explicit error. Completed output is retained
for 15 minutes. Unattached resources also expire after 15 minutes of inactivity.
Larger logs should be written to project files and inspected in bounded slices.
Internal MCP sessions restart their protocol handshake when re-created; their
old unreferenced processes expire rather than receiving speculative replay.

## Project VMs

Exactly one machine or one pool is assigned to a project. VM identity combines
the Zentrale installation, project and physical host. Conversations on the same
project and host share one guest; a pool may hold guests on several hosts.
VM disks, downloaded runtime artifacts and scratch normally use private `/tmp`
storage. On hosts with quota-limited tmpfs, guest disks can use `/var/tmp`.
Ordinary idle shutdown preserves the disk. Remote reboot or `/tmp` cleanup may
discard it, and the next use builds a fresh guest. Project files and explicitly
configured storage are separate mounts.

The authenticated user resets project VMs from the project's three-dot menu.
Agents cannot reset them, gain terminal-only sudo privileges or request host
execution from a VM. Automatic sandbox selection requires the configured VM
isolation and fails when it cannot provide it. See
[Sandboxes, GPUs, and editor](sandbox-and-editor.md) for tooling and permissions.

Disposable evaluation snapshots must receive a fresh installation identity and
remove runtime attachments before starting an engine. `harness-eval` does this
automatically so an evaluation cannot reattach production jobs or daemons.
