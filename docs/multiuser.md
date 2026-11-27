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

Zentrale issues its own revocable session JWTs. They expire after seven days in password mode and after four hours in OIDC mode. The signing key is generated once per installation and encrypted in the database with the existing credential key. Restarts preserve active sessions. The first upgrade from earlier opaque session tokens requires a new login; accounts and credentials are unchanged.

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

Web search starts with Yahoo via the container's DDGS runtime. It does not use Bing RSS or DDGS auto mode. For a second, independent source, set `ZENTRALE_BRAVE_SEARCH_API_KEY` in the server environment. Zentrale uses the official Brave Search API only when Yahoo fails or has no relevant results, so a Yahoo hit does not consume API quota. Outside the container, set `ZENTRALE_DDGS_PYTHON` to the absolute Python executable in a virtual environment with `ddgs` installed. A configured Brave API also works on its own when no DDGS runtime is configured.

Without a Brave key, an unquoted `site:` search can fall back to the public homepage of the named site. Zentrale finds relevant links on that site, without following those links or searching the rest of the web. For example, `Go documentation site:go.dev` can find the manual even if Yahoo returns nothing. For a quoted query, Zentrale instead tries Yahoo once without the quotes after a miss, while still requiring the original phrase and honoring any `site:` restriction. These are separate alternatives, not extra providers tried in succession. An empty result is not proof that no public sources exist, and DDGS results can differ from browser results. Reading a result is a separate request; a site may still refuse automated access or return no readable text.

A quoted name may still match several people, and search engines do not guarantee which article appears in the first results. Add a known detail or use `site:example.org` to restrict the result to a known source. For example, `site:deutsche-handwerks-zeitung.de "Franz Lehnert"` narrows the request to the trade publication and avoids unrelated namesakes. To check the live result, run `TestPublicWebDDGSLiveSmoke` with that query and the expected article URL (`ZENTRALE_TEST_DDGS_QUERY`, `ZENTRALE_TEST_DDGS_URL`, and `ZENTRALE_DDGS_PYTHON`). A live check can still fail if an upstream provider returns no usable results.

Under **Admin settings → Short chats**, choose where code runs and how long chats and VMs are kept. Unpinned chats are deleted after **45 days** by default, counting from creation or the most recent user message. A new user message restarts the full period. Pins prevent automatic deletion. When a pin is removed, the chat keeps whichever expires later: its existing deadline or half the configured period from unpinning. Deleting a chat removes its transcript, attachments, and temporary working files. Detached usage statistics are retained.

Code execution can be disabled, run on one managed MicroVM machine or pool, or run on **Zentrale (local)**. Each user gets one VM only when code execution is needed; chats have separate working directories and commands have no network inside the sandbox. Only explicitly selected attachments are staged for a command. The guest stops after inactivity but retains its disk until its configured lifetime expires (**7 days** from first allocation by default). At expiry, Zentrale removes that VM and its working files without deleting attachments already stored in a chat. The next code command creates a fresh VM. Changing the target does not migrate existing VMs; they must expire and be cleaned up on their old host before new ones can be created on the new target.

On Linux x86-64, the container includes the managed QEMU runner. To enable the local option, start the container with `--init --device /dev/kvm --group-add "$(stat -c %g /dev/kvm)"` in addition to its usual arguments. The container's unprivileged process must be able to open the device; a privileged container is not required. In Compose, use `init: true`, `devices: ["/dev/kvm:/dev/kvm"]` and add the host KVM device's group ID under `group_add`. Zentrale offers the option only when its KVM/runner check passes. The initial code invocation prepares the guest image and needs access to the managed runner's usual artifact mirrors. Local workspaces and guest disks default to `/data/short-chat-vms` in the container (`ZENTRALE_SHORT_CHAT_VM_DIR` overrides this). Give that directory persistent space and keep it private to Zentrale.

## File shares

Administrators can configure read-only SMB, SFTP, and WebDAV shares for short chats. Endpoints take the form `smb://host/share`, `sftp://host:22`, or `https://host/dav`; SFTP configuration also requires the expected SHA-256 host key fingerprint. Prefer HTTPS for WebDAV; HTTP sends credentials and data without transport encryption. Use a dedicated service account with the smallest useful permissions.

Under **Admin settings → File shares**, choose **All users** or **Selected users** for each share. Existing shares remain available to all users until restricted. **Check connection** lists the root using the current draft without saving it. For shares with a per-user view, select an eligible account to test. A successful check with no visible entries is different from a connection or permissions error; the check does not verify the contents of individual files or the search index.

Use `users/{username}` as the root to give each user their own view. The placeholder must be an entire path segment and uses the Zentrale username, not the service account. A fixed root gives authorized chat users the same view. WebDAV servers must enforce their own filesystem containment and must not expose symlinks across user roots. Zentrale lists directories and reads UTF-8 text on the server without starting a VM or giving the model the share credentials. Shares are not available in projects.

For [DomainShare](https://github.com/stuck-lehnert/domainshare/blob/master/docs/agent-api.md), choose **DomainShare** in the same settings, enter the instance's HTTPS address and an agent token issued for the intended AD accounts. The Zentrale account name must be the corresponding AD `sAMAccountName`; use this connector only when administrators or your identity provider control and verify that mapping. Leave root and service username unset. Zentrale sends the chat owner's account name on each request and DomainShare enforces the current AD permissions. Directory browsing, UTF-8 file reads, and DomainShare's filename/full-text search with indexed text reads all run without a VM. The token is encrypted at rest, not returned to the UI or agent, and must be re-entered when changing the endpoint.

Ask in a short chat, for example: “Search the Team Documents share for the travel policy and summarize it.” The model can discover the shares available to the chat owner by name. Zentrale checks access on each new list, search, or read. For DomainShare, both the Zentrale grant and the DomainShare token/AD permissions must allow access. Removing a grant does not remove content already included in chat history.

## Projects and archives

Users need permission to create projects, or membership granted by the owner to use an existing project. Repository selection uses the current user's Git connections. A project's Git and Hugging Face access remains tied to its creator. Setting an emoji makes a project easier to spot in the sidebar; drag the handle to change the active projects' order.

Use managed workspaces and built-in MicroVM targets for projects owned by additional accounts or shared with members. Legacy host and bubblewrap modes do not isolate users from the host filesystem. Such projects remain viewable, but remote execution does not fall back to those modes.

Archiving preserves the conversation history in a separate sidebar list and removes Zentrale-managed remote project workspaces, storage, and VM state. Close editors and terminals first, and save and push the work you intend to keep. Zentrale checks Git worktrees for untracked files or changes and checks that commits are reachable from local remote-tracking refs. It does not fetch or verify availability on the Git server. Ignored files and files outside Git are not backups and are removed with the managed workspace. Interrupted cleanup can be retried from the archive action. Unmanaged workspaces and unsupported sandbox setups cannot be archived safely by Zentrale.
