# Getting started

[← Documentation](README.md)

## Docker

Pushes to `deploy` publish `ghcr.io/lehlud/zentrale` with the `latest` tag and a
tag containing the first 10 characters of the commit SHA. Pull `latest` for the
current release, or replace it with the commit tag to pin a specific build:

```sh
docker pull ghcr.io/lehlud/zentrale:latest
```

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

Run **one Zentrale server per data volume**: agents, jobs and event delivery have
one application owner. SQLite itself supports concurrent connections; Zentrale
uses a normal pool with WAL so reads can run alongside writes. Stop the old
server before starting its replacement. In Coolify, select **Consistent name
(no rolling updates)**. Keep the data volume on local storage. Existing DuckDB
installations must run the [one-time import](#import-an-existing-duckdb) first.

Agent commands and VMs still run on your configured SSH machines. The Zentrale
container needs neither privileged mode nor the Docker socket. Additional server
flags can be appended after the image name, for example
`-origin https://zentrale.example -secure-cookie` behind an HTTPS reverse proxy.

On platforms such as Coolify, keep the image entrypoint unchanged and set these
**runtime** environment variables instead (then redeploy):

```dotenv
ZENTRALE_ORIGIN=https://zentrale.example
ZENTRALE_SECURE_COOKIE=true
```

Use your exact public origin, without an application path. Explicit `-origin`
and `-secure-cookie` flags override the environment defaults. Invalid values
fail startup instead of silently disabling the setting. Zentrale does not trust
arbitrary `X-Forwarded-*` request headers to establish the public origin.

Forward `/terminal/` including WebSocket upgrades through the proxy, as well as
the other application routes. A terminal handshake rejected with HTTP 403 can
mean the public HTTPS origin is missing or mismatched: TLS termination leaves
the backend's connection as HTTP. The terminal error dialog distinguishes this
from an expired session and a missing route. Do not disable origin checks.

### Coolify with GHCR and local KVM

Use [`ghcr-compose.yaml`](../ghcr-compose.yaml) as the Compose definition. It
pulls `ghcr.io/lehlud/zentrale:latest`, keeps `/data` in a named volume, and
exposes the service through Coolify's proxy rather than publishing a host port.

1. The Coolify deployment host needs Linux x86-64 and a working `/dev/kvm`.
   On that host, run `stat -c %g /dev/kvm`. Set `KVM_GID` to the returned
   number in Coolify's **Environment Variables** (for example, `994`).
2. Configure an HTTPS domain for `zentrale`, at the root path.
   [`SERVICE_URL_ZENTRALE_8080`](https://coolify.io/docs/knowledge-base/docker/compose)
   routes the public URL to port `8080` in the container and supplies
   `ZENTRALE_ORIGIN`. Session cookies require HTTPS by default. If you
   intentionally use HTTP for a local test, set `ZENTRALE_SECURE_COOKIE=false`.
3. Select **Consistent name (no rolling updates)** and deploy. If replacing an
   existing installation, keep its current `/data` volume rather than creating
   an empty one. Then enable **Zentrale (local)** under **Admin settings → Short
   chats** if you want to run code locally.

The Compose file requires `/dev/kvm` even if local code execution is disabled in
the app. On hosts without KVM, omit the `devices`, `group_add`, and `KVM_GID`
entries or use the `docker run` example above.

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
in SQLite (`zentrale.sqlite`). Files and commands live on your SSH machines. The Zentrale server
needs to stay available for agents to keep working; your browser can disconnect
after a task has been accepted.

## Install Zentrale as an app

Open Zentrale over HTTPS (or on `localhost`) and use your browser's **Install**
option. In Safari on iPhone or iPad, choose **Share → Add to Home Screen**.
The installed app runs in its own window and still needs a connection to your
Zentrale server. It shows a connection notice when offline; the service worker
does not cache chats, attachments, workspace files or other private data.

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
  -db /var/lib/zentrale/zentrale.sqlite \
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

## Import an existing DuckDB

Stop the old Zentrale server and back up the database and its adjacent
`.credentials.key` file. Keep the original key alongside the source database;
encrypted credentials cannot be recovered without it. The importer never
changes the source or overwrites an existing destination.

From a checkout, with CGO and the C/C++ toolchain installed:

```sh
cd backend
go run -tags duckdb_import ./cmd/import-duckdb \
  -source ../zentrale.duckdb -target ../zentrale.sqlite
```

For Docker, build the new image, stop the old container, then run its included
import tool against the same volume:

```sh
docker stop zentrale
docker run --rm -v zentrale-data:/data \
  --entrypoint /usr/local/bin/import-duckdb zentrale \
  -source /data/zentrale.duckdb -target /data/zentrale.sqlite
```

Start Zentrale with `-db` pointing at the new file (the new image already defaults
to `/data/zentrale.sqlite`). The import copies resources, histories, attachments,
settings, sessions, SSH identities, encrypted credentials and sequence counters.
It prints per-table row counts after success. The old DuckDB remains a backup;
subsequent SQLite writes are not copied back to it.

Startup refuses an old DuckDB passed as `-db`, and refuses to create the default
empty SQLite database alongside an existing `zentrale.duckdb`. A DuckDB lock
error during import means its writer must be stopped first. Do not delete a WAL
to work around it. See [database architecture and operation](database-latency.md)
for concurrency and backup details.
