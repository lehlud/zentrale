# Getting started

[← Documentation](README.md)

## Docker

Build and start from the repository root:

```sh
docker build -t zentrale .
docker run -d --name zentrale --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -v zentrale-data:/data \
  zentrale
```

Open [localhost:8080](http://localhost:8080). The image builds both frontend and
backend; you do not need Go or Node installed on the host.

The container runs as UID/GID `10001`. Its database, WAL, and downloaded browser
editor cache live under `/data`; the named volume preserves them when the
container is replaced. For a bind mount, make the directory writable by that UID.
Treat the volume as private: it contains account credentials and SSH keys.

Agent commands and VMs still run on your configured SSH machines. The Zentrale
container needs neither privileged mode nor the Docker socket. Additional server
flags can be appended after the image name, for example
`-origin https://zentrale.example -secure-cookie` behind an HTTPS reverse proxy.

## Build and run

Install Go 1.27.3 or newer with CGO and a C/C++ toolchain, Node.js 22.12 or newer,
and Make. From the repository root:

```sh
npm ci --prefix frontend
make run
```

Open [localhost:8080](http://localhost:8080). The first visit sets up your login
password and default Git commit name and email.

Zentrale stores configuration, conversations, account credentials, and SSH keys
in DuckDB. Files and commands live on your SSH machines. The Zentrale server
needs to stay available for agents to keep working; your browser can disconnect
after a task has been accepted.

## Your first task

1. **Connect a provider** in Settings and choose defaults for the main agent and
   its independent auto-approver.
2. **Add a machine.** Create an SSH key, configure the host and SSH user, and
   install the public key on that host. Use **Check connection** and verify an
   unfamiliar host's fingerprint before trusting it.
3. **Create a project** and assign the machine. Zentrale prepares a persistent
   project directory when you first connect.
4. **Start a conversation.** Give the agent a task, follow its work, and add
   instructions when needed. You can open the browser editor yourself at any time.

The SSH host needs a POSIX shell and Python 3; Git is needed for repositories.
Managed Fedora VMs have additional [host requirements](sandbox-and-editor.md).
Connection and sandbox preparation are shown before inference starts.

## Run behind HTTPS

Build with `make build`, then run behind your reverse proxy:

```sh
./backend/zentrale \
  -addr 127.0.0.1:8080 \
  -db /var/lib/zentrale/zentrale.duckdb \
  -frontend frontend/dist \
  -origin https://zentrale.example \
  -secure-cookie
```

The database directory must belong to the service user. Database and WAL contain
private keys and provider credentials, so protect them and their backups.
Zentrale uses a private file mask and `0600` database permissions.
`LOGIN_ARGON2ID` can supply an Argon2id PHC password hash without overwriting an
existing password.

## Reconnect

Accepted prompts and queued messages are persisted before remote setup.
Reconnecting reloads their state. Agent input receipt IDs prevent duplicate
delivery when a response is lost and the same submission is retried. Failed
ordinary configuration writes are not automatically replayed.
