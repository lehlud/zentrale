# Multi-user operation

Make a backup of the database and its credential key before upgrading an existing installation. Existing projects, Git connections, and commit identity remain with the initial administrator. Existing single-user passwords still work; sign in again as `admin`. Old session cookies are not reused.

## Sign-in and administration

Use either password login or OpenID Connect, not both. On a fresh password installation, the account created during setup is the administrator. `LOGIN_ARGON2ID` can seed that account as `admin` instead.

For a fresh OIDC installation, configure these environment variables and register the callback with the identity provider:

```sh
ZENTRALE_OIDC_ISSUER=https://login.example.org/realms/team
ZENTRALE_OIDC_CLIENT_ID=zentrale
ZENTRALE_OIDC_CLIENT_SECRET=…
ZENTRALE_OIDC_REDIRECT_URL=https://zentrale.example.org/auth/oidc/callback
```

Sign in before opening the installation to other users: the first verified identity becomes the administrator. Signed-out users go directly to the identity provider. After bootstrap, an administrator adds other users with their OIDC subject (`sub`), not their email address. An initialized database cannot switch authentication mode or OIDC issuer without an explicit migration.

Administrators manage accounts, project creation permission, provider/model availability, and monthly UTC token and USD budgets in Settings. A limit of zero means unlimited. Usage is recorded separately for input, cached input, cache writes, and output. The Usage page distinguishes known charges from estimates and missing pricing. Project activity counts toward the creator's budget, even when a member ran it. Budget enforcement uses reported or estimated model usage; it does not cap charges that happen outside Zentrale.

## Short chats

Short chats are private to their owner, without a Git project, VS Code, or a terminal. Persistent goals and subagent delegation are available only in projects. Attachment uploads are accepted on Zentrale itself (up to 30 files per message, 25 MiB each and 100 MiB total). Web lookup and reading text from attachments or shares do not boot a VM. The optional Humanizer uses the global setting.

An administrator can enable code execution by selecting one managed MicroVM machine or pool under Short-chat settings. A user is pinned to one pool member on first use. Approved code starts that user's VM on demand, with a separate working directory for each chat and no network inside the command sandbox. Only attachments explicitly selected for the command are staged in its temporary attachment directory. The guest stops after inactivity and keeps its disk for reuse. Changing the target does not move a user's existing workspace automatically.

## File shares

Administrators can configure read-only SMB, SFTP, and WebDAV shares for short chats. Endpoints take the form `smb://host/share`, `sftp://host:22`, or `https://host/dav`; SFTP configuration also requires the expected SHA-256 host key fingerprint. Prefer HTTPS for WebDAV; HTTP sends credentials and data without transport encryption. Use a dedicated service account with the smallest useful permissions.

Use `users/{username}` as the root to give each user their own view. The placeholder must be an entire path segment and uses the Zentrale username, not the service account. A fixed root intentionally exposes the same view to all chat users. WebDAV servers must enforce their own filesystem containment and must not expose symlinks across user roots. Zentrale lists directories and reads UTF-8 text on the server without starting a VM or giving the model the share credentials. Shares are not available in projects.

## Projects and archives

Users need permission to create projects, or membership granted by the owner to use an existing project. Repository selection uses the current user's Git connections. A project's Git and Hugging Face access remains tied to its creator. Setting an emoji makes a project easier to spot in the sidebar; drag the handle to change the active projects' order.

Use managed workspaces and built-in MicroVM targets for projects owned by additional accounts or shared with members. Legacy host and bubblewrap modes do not isolate users from the host filesystem. Such projects remain viewable, but remote execution does not fall back to those modes.

Archiving preserves the conversation history in a separate sidebar list and removes Zentrale-managed remote project workspaces, storage, and VM state. Close editors and terminals first, and save and push the work you intend to keep. Zentrale checks Git worktrees for untracked files or changes and checks that commits are reachable from local remote-tracking refs. It does not fetch or verify availability on the Git server. Ignored files and files outside Git are not backups and are removed with the managed workspace. Interrupted cleanup can be retried from the archive action. Unmanaged workspaces and unsupported sandbox setups cannot be archived safely by Zentrale.
