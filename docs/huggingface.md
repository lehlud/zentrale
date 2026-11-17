# Hugging Face connection

## Design and official references

The integration supports both **browser OAuth** and a validated **user access
token**. Neither method sets an implicit global `HF_TOKEN` in VM commands.
Token login requires no OAuth app and supports repository-specific fine-grained
read scopes. Browser login uses an explicitly configured Hugging Face OAuth app:

- [User access tokens](https://huggingface.co/docs/hub/security-tokens)
- [Gated datasets](https://huggingface.co/docs/hub/datasets-gated)
- [OAuth and device flow](https://huggingface.co/docs/hub/oauth)
- [Downloading files](https://huggingface.co/docs/huggingface_hub/guides/download)

## Browser OAuth

1. Open **Settings → Hugging Face → Actions → Configure OAuth app**. The
   configuration opens in a modal;
   register an app at <https://huggingface.co/settings/applications/new>.
2. Register the exact callback URL displayed by Zentrale, including any preview
   or reverse-proxy prefix. See `integrations.md` for public-origin setup.
3. Enter the client ID. Public apps use PKCE without a secret; for a confidential
   app, disable **Public OAuth app without a client secret** and enter its secret.
   An empty secret retains the saved secret only for the same client ID. Save.
4. Select **Sign in with Hugging Face**, open the consent link and approve at
   Hugging Face. The callback submits the grant using the initiating Zentrale
   session. Return to the original settings page after confirmation.

Zentrale requests only `openid profile read-repos gated-repos`: profile identity,
read access to personal private repositories, and public gated repositories
which the user has already been permitted to access. Select organization access
explicitly on Hugging Face's consent screen. OAuth is not repository-specific;
use a fine-grained token below when narrower access is needed. No write,
inference, billing, email or `request-gated-repos` scope is requested, and Zentrale
does not accept dataset terms or submit access requests on your behalf.

Authorization Code + PKCE S256 uses the shared same-origin, session-bound
callback. The one-time verifier is consumed before exchange and codes are never
retried after a lost response. App credentials, tokens and pending state share
one encrypted record. Identity comes from the fixed `/oauth/userinfo` endpoint,
not an unverified ID-token payload. Token/userinfo requests never follow redirects.

Before a dataset request, expiring OAuth access is refreshed if Hugging Face
issued a refresh token; omitted rotated refresh tokens retain the previous one.
An expired grant without refresh capability or an `invalid_grant` requires a new
sign-in. Temporary network errors preserve the connection for retry. No
undocumented `offline_access` scope or ambient CLI/SDK credential is assumed.

Restarting or cancelling a pending sign-in does not disconnect the currently
connected account. Switching OAuth apps disconnects an OAuth account and cancels
its active transfers, but preserves an existing personal-token connection.
Clearing the client ID disables OAuth. Replacing either login method invalidates
older pending callbacks, so a late response cannot restore the previous account.

## Personal access token

1. Open **Settings → Hugging Face → Connect → Access Token** and follow the
   token-settings link in the dialog.
2. Prefer a fine-grained **read** token limited to the repositories needed for
   the task. Include gated-repository read access if applicable. Do not grant
   write permissions merely to download datasets.
3. For gated repositories, accept the author's terms or obtain approval on
   Hugging Face yourself. A token cannot replace account approval; organization
   policy may additionally require approval of that token.
4. Enter the token in the password field, **not in agent chat**. Zentrale checks
   the account with `whoami-v2`, stores the credential, then clears the input.
   Connected status records that validation, not a perpetual permission grant.
5. Ask the agent for the dataset normally. It discovers the dataset tools and
   uses the normal approval/network/job machinery. Anonymous public downloads
   also work without connecting an account.

Choose **Manage → Access Token** to replace a token or switch from OAuth.
Closing the dialog discards unsaved token input.
**Actions → Disconnect** deletes the local account tokens and pending login and cancels
active Hub requests, but retains downloaded data and the OAuth app configuration.
It does not revoke a personal token or OAuth grant at Hugging Face; revoke these
there when appropriate.
An old pending login cannot reconnect the account after disconnect/replacement.

## Agent workflow

`huggingface_status {}` returns only public connection metadata.
`huggingface_files` accepts a dataset ID, revision (default `main`), optional
prefix/recursive traversal, limit (default 20, maximum 100), and returned cursor.
It returns bounded file metadata and an LFS SHA-256 when available. Follow
`hasMore`/`nextCursor`; a page is not an exhaustive snapshot.

`huggingface_download` retrieves **one selected file**, not an automatically
executed dataset loader or implicit complete repository snapshot. For example:

```json
{
  "dataset": "organization/dataset",
  "revision": "main",
  "file": "data/train.parquet",
  "destination": "data/dataset/train.parquet",
  "maxBytes": 104857600,
  "timeout_ms": 600000,
  "background": true,
  "network": true
}
```

Use a pinned commit revision for reproducibility and pass the metadata's
`sha256` when available. Check storage/quota before large downloads, and set a
realistic explicit byte/deadline budget. `machineId` selects an available
configured machine; destination is literal and project-relative. External
storage paths, automatic archive extraction and whole-snapshot synchronization
are deliberately not part of this file tool.

Use returned job IDs with ordinary `job_logs`, `job_wait`, `job_status` and
`job_stop`. No interrupted call is automatically replayed, resumed or allowed to
overwrite a destination. Success reports path, byte count and computed SHA-256;
file contents are not copied into model context. Local Parquet/CSV/etc. files
can subsequently be used by dataset libraries through separately reviewed
execution. The connection does not implicitly authenticate every SDK/shell in
the VM.

## Credential storage and backups

HF credentials use the separate `credential_records` table, **not JSON
resources/settings**. AES-256-GCM with independent random nonces encrypts each
record and authenticates its name. No resource/GraphQL/model query returns that
table or its key. Public API fields contain only connection metadata.

For a persistent database the key is a private, non-symlink 32-byte file named
`<resolved-database-path>.credentials.key`, created atomically with mode `0600`.
**Back up and restore this file together with an application-consistent database
backup**, keeping both private. Do not arbitrarily copy a live database/WAL or
commit the key to Git. If encrypted records exist and their key cannot be loaded
after restart, reads fail closed instead of generating a replacement key and
silently losing credentials. An in-memory test database uses an in-memory key.

Encryption protects a database copy without its separate key and prevents
generic resource access to credentials. It is not an HSM and does not protect
against someone controlling the application process or stealing both files.
Existing unrelated credential implementations are not silently migrated by
this integration.

## Security and lifecycle contracts

- Settings: both browser OAuth and fine-grained/read token login, verified
  account name, app setup and disconnect. Never persist secrets in
  browser drafts/localStorage, return a token to GraphQL, expose it to model
  context, or include it in commands, logs or job records.
- Backend service: encrypted credential record, validate PATs against the fixed
  `https://huggingface.co/api/whoami-v2` endpoint and OAuth through
  `/oauth/userinfo`, bounded responses, sanitized
  errors, no arbitrary API origins. Disconnect/replacement cancels active
  authenticated requests; an in-flight older login cannot resurrect a deleted
  connection. Public metadata explicitly records validation time, not a promise
  that permissions can never expire.
- Tools: local connection status; reviewed/network-authorized dataset file
  listing (bounded pagination, optional recursive listing); reviewed download
  of a selected dataset file to a project-relative destination. Anonymous public
  reads remain possible. Existing dataset approval/token permissions are still
  required for private/gated data. Never automatically accept terms, request
  access, submit user profile data or bypass gating.
- Downloads reuse normal background jobs, deadlines, cancellation, machine
  selection, sandbox preparation and no-replay behavior. A lazy job-scoped input
  opener retrieves HTTPS bytes on the application side, then streams framed
  bytes over the existing SSH/sandbox stdin path. Credentials never enter the
  remote command/environment. Limit bytes explicitly; optionally require a
  SHA-256 digest. List metadata includes LFS SHA-256 when available.
- Follow only HTTPS Hugging Face/CDN redirects with a bounded redirect count;
  remove authorization on every cross-origin redirect. Never return signed CDN
  URLs. Authentication/permission failures at HF must not log the user out of
  Zentrale or expose upstream response bodies.
- The fixed Python writer accepts data, not code: project-relative literal
  paths, no traversal/symlink parents, exclusive destination creation, bounded
  streaming frames, mandatory completion trailer with byte count and digest.
  Partial/failed transfers must not become completed files. Stage in the target
  directory and link exclusively only after verification; never overwrite an
  existing destination. A hard VM/process kill may leave a staging file, not a
  falsely successful destination. No archive extraction or dataset code runs.
- Agent checks existing storage/quota before large downloads. The tool uses an
  explicit byte limit, bounded buffers and ordinary filesystem quota failures,
  not large application-side temporary files. No large real datasets are needed
  for integration tests.

## Verification

Dedicated tests cover encrypted/non-resource storage, authenticated record
names/tampering, persistence, missing/insecure/symlink keys; account validation,
disconnect/login races, shutdown, auth/CDN error secrecy, pagination including
the Hub's escaped Link relation quotes, stream limits and cancellation; writer
digest/truncation/extra-data/size/path/symlink/no-overwrite behavior; lazy input
opening and one-time cleanup; actual fixed-writer transfer through the SSH test
peer into a project; authenticated credential-free GraphQL outputs; native
settings connect/replace/disconnect, DE/EN, keyboard submission, no browser
credential persistence, permission-failure retry without logging out of
Zentrale, and server-pushed status/error snapshots.

The public `lhoestq/demo1` tree API was also checked anonymously with `limit=2`
(bounded metadata only). Gated/private behavior uses synthetic accounts and
responses: no real personal credential, terms acceptance, access request, large
dataset download or live gated-account claim is needed for these tests.

Local verification for this implementation:

| Check | Result / ignored artifact log |
| --- | --- |
| All Go packages | PASS, `hf-go-full.log` |
| `go vet ./...` | PASS, `hf-vet.log` |
| Targeted HF/datastore/agent/webapi race checks | PASS, `hf-race-final.log` |
| Python: CI scripts / agent scripts / remote scripts / MicroVM | PASS: 12 / 64 / 20 (one skip) / 93; `hf-python-*.log`, `hf-python-microvm-final.log` |
| Recovery-fixture concurrency regression | PASS 100 repetitions, `hf-recovery-race-repeat.log` |
| Frontend build and formatting | PASS, `hf-build-final.log`, `hf-format-check.log` |
| GraphQL schema and generated types | Deterministic; `hf-schema-final.json`, `hf-types-final.log` |
| Browser whole-suite run | 227/228 passed, `hf-browser-full.log`; one existing lazy-diff viewport assumption failed |
| After fixing the two existing test races | All eight focused conversation/HF/confirmation cases passed, `hf-browser-regressions-final.log`; full MicroVM suite passed |

Logs are under `frontend/artifacts/recovery/`. The recovery fixture now waits
for the real stdin EOF marker before deleting its mailbox; the conversation
fixture scrolls the lazy diff boundary into view before expecting more rows.
Neither production recovery nor lazy rendering was weakened, and no assertion
was removed. The whole 228-case browser suite was not rerun after that test-only
correction. These are local results, not a deployment or newly green remote-CI
claim. No dependency changes, new subagents, commits or pushes were required.

## Local verification, 2026-11-17

The token connection was exercised through the actual local GraphQL API with
an explicitly supplied credential and a disposable encrypted database. Public
connection metadata exposed the validated account and authentication mode while
keeping the credential private.

The live check against
[`StonyBrook-CVLab/doc3D-dataset`](https://huggingface.co/datasets/StonyBrook-CVLab/doc3D-dataset)
covered a bounded root listing, five files under `doc3d/` with a continuation
cursor, the 1,462-byte README through Zentrale's framed download, and an HTTP
`206` response for bytes `0–255` of `doc3d/alb_1.zip` (289,092,989 bytes total).
The archive sample used the production HTTP client's redirect policy. The
follow-up disconnect returned `connected: false`.

This exercised the personal access token path with a small, fixed data sample.
OAuth setup and callback behavior are covered separately by browser fixtures.
