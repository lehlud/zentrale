# Multi-user operation

## Settings and permissions

Personal settings contain Git/platform connections and the account's commit
identity. A project's override takes precedence over its owner's identity. The
legacy installation identity belongs to the initial account; other users set
their own, even when editing projects that already exist.

Admin settings contain user management, the global Humanizer and file-share
configuration, and separate released model presets for conversations and
projects. Each preset pairs a provider model with a fixed thinking level. Admins
set the two sets of defaults and may override them for themselves in personal
settings. Everyone can switch a conversation to a released preset for its own
scope. The short-chat code reviewer uses the configured reviewer default rather
than the conversation's model selector. The provider-level model restriction also
applies. Revoking a preset prevents further use, even if a conversation still
displays that selection. Until an admin first saves a scope's presets, legacy
defaults remain available to ordinary users and existing selections still work.

Machines and pools are global. Admins can grant them individually under **Admin
settings → Users → Machine access**. A regular user needs both project creation
permission and a target grant to create a project. The same grant is needed when
moving to a new target; existing assignments and project membership are not
affected by grant changes. Short chats use the separately configured shared
runtime, not these grants.

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

Administrators manage accounts, project creation permission, provider/model availability, and monthly UTC token and USD budgets in **Admin settings**. A limit of zero means unlimited. Usage is recorded separately for uncached input, cached input, cache writes, and output. The admin-only **Usage** page distinguishes known charges from estimates and missing pricing. Hover a segment or focus it with the keyboard to see its value. Project activity counts toward the creator's budget, even when a member ran it. Budget enforcement uses reported or estimated model usage; it does not cap charges that happen outside Zentrale.

## Short chats

Short chats are private to their owner, without a Git project, VS Code, or a terminal. Persistent goals, subagent delegation, and slash commands such as `/side` are available only in projects. Attachment uploads are accepted on Zentrale itself (up to 30 files per message, 25 MiB each and 100 MiB total). Web lookup and reading text from attachments or shares do not boot a VM. The optional Humanizer uses the global setting.

Use the pencil at the top of the sidebar to start a chat. The **Conversations** icon opens private chat history and pins; the **Projects** icon opens the project list, with conversations nested under each expandable project. Personal and admin settings remain in their own pop-up menus at the bottom. The first message supplies the chat title. Short chats use the same provider caching integration as projects, with a separate cache key for each conversation and a smaller system prompt. Cache hits still depend on the provider, the model, and how much context is reused.

The ring next to the composer shows approximate context use. Open it to see used and available tokens and the selected model's limit. This is the current window, not total token consumption. Both short chats and project conversations compact context automatically. Project agents with a vision-capable model can use `view_image` to inspect regular PNG or JPEG files from their workspace (up to 8 MiB and 20 megapixels per image). The reviewed image is sent as a native image input for the current turn; its bytes are not stored in the transcript.

Zentrale queries Bing's public RSS results first. Only when Bing fails or has no relevant matches does it try one other provider: Yahoo via the container's DDGS runtime. DDGS auto mode is not used. Outside the container, set `ZENTRALE_DDGS_PYTHON` to the absolute Python executable in a virtual environment with `ddgs` installed to enable this fallback; otherwise only Bing is queried. The previous Brave API key setting is no longer used. Empty results report unavailable providers separately and do not prove that no public sources exist. Quoted phrases must appear in the title or snippet; unrelated results are discarded. Reading a result is a separate request; a site may still refuse automated access or return no readable text.

An administrator can enable code execution by selecting one managed MicroVM machine or pool under Short-chat settings. A user is pinned to one pool member on first use. Approved code starts that user's VM on demand, with a separate working directory for each chat and no network inside the command sandbox. Only attachments explicitly selected for the command are staged in its temporary attachment directory. The guest stops after inactivity and keeps its disk for reuse. Changing the target does not move a user's existing workspace automatically.

## File shares

Administrators can configure read-only SMB, SFTP, and WebDAV shares for short chats. Endpoints take the form `smb://host/share`, `sftp://host:22`, or `https://host/dav`; SFTP configuration also requires the expected SHA-256 host key fingerprint. Prefer HTTPS for WebDAV; HTTP sends credentials and data without transport encryption. Use a dedicated service account with the smallest useful permissions.

Use `users/{username}` as the root to give each user their own view. The placeholder must be an entire path segment and uses the Zentrale username, not the service account. A fixed root intentionally exposes the same view to all chat users. WebDAV servers must enforce their own filesystem containment and must not expose symlinks across user roots. Zentrale lists directories and reads UTF-8 text on the server without starting a VM or giving the model the share credentials. Shares are not available in projects.

For [DomainShare](https://github.com/stuck-lehnert/domainshare/blob/master/docs/agent-api.md), choose **DomainShare** in the same settings, enter the instance's HTTPS address and an agent token issued for the intended AD accounts. The Zentrale account name must be the corresponding AD `sAMAccountName`; use this connector only when administrators or your identity provider control and verify that mapping. Leave root and service username unset. Zentrale sends the chat owner's account name on each request and DomainShare enforces the current AD permissions. Directory browsing, UTF-8 file reads, and DomainShare's filename/full-text search with indexed text reads all run without a VM. The token is encrypted at rest, not returned to the UI or agent, and must be re-entered when changing the endpoint.

## Projects and archives

Users need permission to create projects, or membership granted by the owner to use an existing project. Repository selection uses the current user's Git connections. A project's Git and Hugging Face access remains tied to its creator. Setting an emoji makes a project easier to spot in the sidebar; drag the handle to change the active projects' order.

Use managed workspaces and built-in MicroVM targets for projects owned by additional accounts or shared with members. Legacy host and bubblewrap modes do not isolate users from the host filesystem. Such projects remain viewable, but remote execution does not fall back to those modes.

Archiving preserves the conversation history in a separate sidebar list and removes Zentrale-managed remote project workspaces, storage, and VM state. Close editors and terminals first, and save and push the work you intend to keep. Zentrale checks Git worktrees for untracked files or changes and checks that commits are reachable from local remote-tracking refs. It does not fetch or verify availability on the Git server. Ignored files and files outside Git are not backups and are removed with the managed workspace. Interrupted cleanup can be retried from the archive action. Unmanaged workspaces and unsupported sandbox setups cannot be archived safely by Zentrale.
