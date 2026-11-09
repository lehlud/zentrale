# Machines and pools

[← Documentation](README.md)

## Start with SSH

Add a host, SSH user, and key under Machines. Jump hosts can reference configured
machines. **Check connection** tests authentication and reports the detected
sandbox. Unknown host keys require an explicit trust decision.

Managed projects get a stable `project-<project-id>` directory below
`workspace_root`, which defaults to `~/.local/share/zentrale/projects` in the SSH
user's home. Renaming a project does not move its files. Explicit project paths
are also supported. Standalone machines have independent workspaces; switching
machines does not copy files.

## Import a pool

Pools group machines that share persistent project storage. Open
**Machines → Pools → Import / update YAML**:

```yaml
id: training
name: Training pool
max_machines: 2
shared_home: true
workspace_root: "~/.local/share/zentrale/projects"
single_user: true
sandbox: auto
machines:
  - id: worker-a
    name: Worker A
    host: worker-a.example.org
    user: developer
  - id: worker-b
    name: Worker B
    host: worker-b.example.org
    user: developer
```

YAML accepts one document and rejects unknown fields. Zentrale generates one
dedicated SSH identity for a new pool; key IDs cannot be supplied. Copy its public
key from the pool card into the members' `authorized_keys`. Updating the same
pool keeps its identity.

**Check connection** on a pool tests a fresh random sample: 10% of its members,
rounded up, with a minimum of 3 and a maximum of 8. Pools with fewer than 3 members
are checked in full. Each selected member gets its own SSH and sandbox result.
Retrying a failed member or confirming its host key checks that same machine;
checking the whole pool draws a new sample.

The workspace root must identify the same shared filesystem on every member.
Zentrale checks a project marker on failover and rejects a missing share rather
than creating an empty replacement. Pool limits count concurrent host use across
agents and background jobs. `single_user` checks for other logged-in users before
and during execution.

## Host keys and certificate authorities

The optional `known_hosts` block accepts verified OpenSSH records, including
multiple keys, host patterns, hashed names, `@cert-authority`, and `@revoked`:

```yaml
known_hosts: |
  # Paste verified OpenSSH host-key or CA records here.
```

This block governs the pool's connections; global trust does not override it.
Certificate checks include hostname, signature, validity, and revocation. Without
the block, use the normal explicit host-key trust flow.

The [FAU CIP example](../fau-cip-pool.yaml) includes its CA. Adapt the SSH user and
user-specific storage path before importing it elsewhere.

## Storage and quota

Machines and pools can expose extra storage through the integrated VM runner:

```yaml
storage:
  - id: scratch
    path: /srv/scratch/developer
    shared: true
    description: Temporary data; keep important results elsewhere.
quota_command: [/usr/local/bin/user-quota]
```

Only this project's subdirectory is mounted at `/mnt/zentrale-storage/scratch`.
Other user files are not exported. Shared storage uses project marker checks;
changing a default path does not move an established directory. VM reset and
project removal do not automatically delete these files.

`storage_list` gives exact mounts. `storage_quota` runs only the configured fixed
argument vector on the SSH host, independently reviewed, without starting a VM.
It accepts no model-supplied host command. Account quota may include unrelated
files outside the project's mounts.

Agents are instructed to check capacity before large datasets and checkpoints.
Cleanup must preserve valuable results, active checkpoints, and unrelated data.
For FAU CIP, use `/proj/ciptmp/<user>` and `quota_command: [/usr/bin/cip-quota]`.

## Parallel work and failover

`exec_command {cmd, gpu:true, background:true}` can reserve separate prepared GPU
hosts for independent jobs. Optional `machineId` pins a job without changing the
default machine. The harness allows at most one requested GPU job per host and
honors pool limits. GPU labels alone are insufficient: configure
[q35 and VFIO](sandbox-and-editor.md#gpu-passthrough).

The agent decides how to split the task and is instructed to use parallelism when
it helps. Use separate output and checkpoint directories. Failover preserves
shared project files, but does not move guest disks or processes. Started
commands are never automatically replayed; agents resume from logs and checkpoints.
