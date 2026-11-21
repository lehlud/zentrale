# Model and Git connections

[← Documentation](README.md)

Settings shows current model defaults, the commit identity, and connection
status. Account setup opens in a modal. Edit the commit identity from its
summary card; the additional GitHub API token lives under **Advanced API access**.
See [Hugging Face](huggingface.md) for dataset connections.

## Choose your models

A provider is an account or endpoint, rather than a fixed model. Connect it in
Settings, then choose defaults for the main agent and auto-approver independently.
New conversations inherit those defaults; existing ones retain their selections.

Model choices also include a thinking/reasoning level. The main-agent default is
`xhigh` (or the highest supported level); the approver default is `medium`.
`regular` and `default` are equivalent to medium. The chooser exposes supported
levels and preserves the selection across reloads. For models whose supported
levels are unknown, the selector remains editable: explicit manual overrides are
preserved and sent to the provider, which may reject unsupported values. Without
an explicit override these models continue to use the provider's defaults.

| Provider        | Connection                                         |
| --------------- | -------------------------------------------------- |
| OpenAI          | API key or ChatGPT device login with token renewal |
| OpenRouter      | API key or browser login with PKCE                 |
| Anthropic       | API key                                            |
| Google / Gemini | API key or own-app Google API OAuth with renewal   |
| Mistral         | API key                                            |
| OpenCode        | API key                                            |
| LiteLLM         | Configurable API endpoint, optional key            |

Model lists come from the connected account. Refresh or search them in the UI.
For OpenAI device login, device authentication must be enabled by the ChatGPT
account or workspace. Zentrale uses native
[OpenAI login](https://developers.openai.com/codex/auth) and
[OpenRouter PKCE](https://openrouter.ai/docs/guides/overview/auth/oauth), without
importing local CLI sessions.

### Google API browser sign-in

Select **Google / Gemini → Google API account**. This is a login to the Gemini
Developer API, not a way to use an AI Pro/Ultra subscription or Gemini CLI
allowance. The endpoint remains `generativelanguage.googleapis.com/v1beta`;
this does not configure Vertex AI.

1. Create a Google Cloud project, enable the Gemini/Generative Language API and
   configure the OAuth consent screen. Add your account as a test user if the
   app is still in testing; Google may require verification before public use.
2. Create an OAuth client of type **Web application**. Register the exact callback
   URL displayed by Zentrale, including any preview/reverse-proxy path prefix.
3. Enter its client ID, client secret and API quota/billing **project ID** in
   Zentrale. The signed-in account must be permitted to use that project and API,
   including `serviceusage.services.use` where required. Configure billing as
   required by Google's API tier; free-tier quotas, paid usage and regional
   availability remain Google's rules, not subscription entitlements.
4. Save, open the Google consent page and approve. Return to the original dialog
   after the callback confirms submission. Available models load automatically.

Zentrale uses Authorization Code + PKCE S256, requests offline access, and renews
access tokens with the refresh token. Following Google's Gemini API OAuth guide,
the requested scopes are `cloud-platform` and `generative-language.retriever`
under `https://www.googleapis.com/auth/`. These are broad API scopes: use an
account/project with only the permissions you intend to grant. No profile/email
or unrelated consumer-subscription access is requested. Inference and model
discovery use `Authorization: Bearer` and `x-goog-user-project`, not an API key.
Google OAuth cannot be sent to an arbitrary custom provider endpoint.

Testing-mode refresh tokens can expire according to Google's OAuth policy. If
access is revoked or refresh is rejected, sign in again; temporary transport
failures do not cause an authorization-code replay. **Disconnect** removes the
account tokens locally while retaining the app configuration for a future login.
To revoke the grant at Google as well, remove Zentrale's own OAuth app in your
Google account permissions. No ambient gcloud/ADC/CLI tokens are imported.

Official contracts: [Gemini API OAuth](https://ai.google.dev/gemini-api/docs/oauth),
[Google web-server OAuth](https://developers.google.com/identity/protocols/oauth2/web-server),
and [Gemini API billing](https://ai.google.dev/gemini-api/docs/billing).

### Anthropic and Claude subscriptions

Anthropic remains an **API-key** connection. Anthropic explicitly prohibits
third-party applications from offering a Claude.ai subscription login or
collecting, storing or intermediating Free/Pro/Max/Ultra account credentials.
Zentrale therefore does not import Claude Code sessions or present a misleading
Claude-plan login button. Use an Anthropic API key, or configure an already
supported compatible inference provider separately. Running the unmodified
Claude Code program with its own user authentication is a different integration
and is not implemented by this provider connection. See Anthropic's
[authentication](https://code.claude.com/docs/en/authentication) and
[legal/compliance](https://code.claude.com/docs/en/legal-and-compliance) documentation.

Missing context sizes use a marked 32,000-token estimate. Context sizes and
prices can be edited from the model's menu; overrides survive catalog refreshes.
Prices are USD per million tokens. Usage separates metered costs from
API-equivalent costs for fixed-price accounts and marks estimated or missing
token and cache data.

### Interrupted model connections

Zentrale automatically retries connection failures, timeouts, interrupted response
streams, and temporary provider errors, with at most four attempts. Backoff grows
from roughly one to two to four seconds; a provider's `Retry-After` is respected
within a 30-second total backoff budget. Healthy streams have no fixed overall
deadline. Five minutes without incoming data cancels the stalled attempt.

The conversation shows when a retry starts and replaces the unfinished answer
draft. Only a complete response enters conversation history or starts tools, so
repeating a model request does not repeat environmental actions. Failed attempts
may still be billed by the provider; their unavailable usage is not included in
the completed response's token counts. Stopping the agent cancels retries.
Invalid credentials, billing limits, and permanent response errors are not retried.

## Connect GitHub, GitLab, Gitea or Forgejo

Git connections are available in Settings or directly in the project/source
repository picker. Register your own OAuth application first:

- **GitHub:** enable Device Flow and provide the client ID. Requested scopes are
  `repo read:org workflow`. The additional `workflow` grant allows changes to
  GitHub Actions workflow files, including pushes using OAuth-created deploy keys.
- **GitLab:** provide a client ID, `api` scope, and a client secret for confidential
  applications. The instance must support Device Flow. Self-hosted HTTPS
  instances can be configured.
- **Gitea 1.23 or newer:** add an instance in Settings, enter its HTTPS origin,
  display name and app client ID, and register the exact displayed callback URL
  on that instance. Authorization Code + PKCE S256 works with a public app
  (no secret) or a confidential app with a client secret. Requested scopes are
  `read:user write:repository write:organization` for account identity, repository
  creation and deploy-key management. Older/unrecognized versions are rejected:
  silently ignoring unsupported scope names could grant full account access.
- **Forgejo:** the same instance/app/PKCE setup applies, but Forgejo currently
  does **not** restrict OAuth tokens using scopes. Consent grants broad account
  access; the UI warns explicitly. Zentrale keeps that token in the backend and
  uses repository-scoped SSH deploy keys for agents and Git operations.

Add multiple Gitea/Forgejo instances (up to 32 custom connections); their IDs,
names, origins and account grants are independent. The repository picker shows
the instance name/host. Editing an existing connection cannot change its origin:
add a separate connection for another server. Both forges support repository
selection/creation, organization namespaces and read/write deploy-key management.
No access tokens are installed in the VM, editor, agent environment or model
context. Provider repository/organization permissions are still authoritative.

Instance URLs must be HTTPS origins, not arbitrary API paths. Private, loopback
and carrier-grade NAT addresses are denied unless **Allow private-network
connections** is explicitly enabled for that trusted instance. Link-local,
known metadata, reserved and address-transition destinations remain blocked.
The backend validates the complete DNS answer on every dial and connects to the
validated IP directly; environment proxies and HTTP redirects are disabled.
Internal instances still need a certificate trusted by the Zentrale backend;
there is no insecure-TLS switch. See the official
[Gitea OAuth](https://docs.gitea.com/development/oauth2-provider) and
[Forgejo OAuth](https://forgejo.org/docs/latest/user/oauth2-provider/) contracts.

Enter app configuration in the UI or through `GITHUB_CLIENT_ID`,
`GITLAB_CLIENT_ID`, optional `GITHUB_CLIENT_SECRET` / `GITLAB_CLIENT_SECRET`, and
`GITLAB_BASE_URL`. GitHub/GitLab retain their existing built-in connection each;
the environment variables apply to those connections only. See the setup docs for
[GitHub](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps#device-flow)
and [GitLab](https://docs.gitlab.com/api/oauth2/#device-authorization-grant-flow).

### Browser callback and credential storage

For Gitea, Forgejo and Google, the callback page immediately removes code/state
from the address bar, is served with `no-store`/`no-referrer`, and completes the
grant through an authenticated same-origin POST. The normal HttpOnly,
SameSite=Strict application cookie is not weakened for external redirects.
Random state, PKCE, a ten-minute expiry and the initiating application session
must all match. A different session, logout, cancellation, superseding login,
configuration edit or consumed verifier prevents code reuse. Do not close the
original login dialog before completing consent: closing it cancels that flow.
Configure Zentrale's external origin/cookie security correctly behind a proxy.

Git account credentials, new Google app/account credentials and their pending
PKCE flows use the encrypted credential store. Ordinary resource/GraphQL snapshots
contain only public status. Existing Git account records migrate on their first
normal load. This is not secure erasure of old database pages, WAL or backups.
Back up the database consistently together with its adjacent
`<database-path>.credentials.key` (private mode `0600`); losing that original key
fails closed. This protects a database-only disclosure, not theft of both the
database and key or control of the running backend. Never commit either file.

## Repositories and keys

Interrupted initial clones are recovered automatically. Zentrale resumes a
completed checkout's publication or retries its download using available local
Git objects. Previous attempts remain in `.git/zentrale-clone-recovery`; existing
project files are preserved when a name conflicts.

Select an existing repository or create one when adding a project or source.
The picker requires permission to manage deploy keys and respects organization
policies. New repositories are private. Before the agent's first model request
or the editor opens, Zentrale clones the selected repository into the project
directory and additional sources into their named subdirectories. Existing
checkouts are reused without fetching, pulling or resetting; local changes stay
intact. A nonempty directory without a checkout or with a different origin stops
preparation with an error. Checkout failures keep the submitted task available
for retry. Agents use `source_git` for subsequent Git operations.

Zentrale generates a dedicated read/write deploy key per repository. Projects
using the same repository share its binding. Interrupted key registration stays
pending and is checked on retry. SSH host-key verification still applies. For
GitHub, Zentrale loads host keys over HTTPS from the official
[metadata API](https://docs.github.com/en/rest/meta/meta#get-github-meta-information)
and scopes them to GitHub's SSH host. For other Git hosts, confirm the
fingerprint in Zentrale. The confirmation is stored for the host and port and
applies to existing checkouts, including Git in the VM terminal and VS Code.
Use **Check Git connection** in the project menu or VM terminal after a host key
change. Confirming a new key for an algorithm replaces that algorithm's pin and
preserves the endpoint's other confirmed algorithms.

The Keys page lists machine, pool, and repository uses. Referenced keys cannot be
deleted. Removing a source releases its reference without revoking a key needed
by another project. After all references are gone, **Revoke repository access
and delete key** removes the remote key and then its local identity. The original
Git account must be connected.

## Commit identity

Set a global name and email in Settings. A project can override both, or leave
both empty to inherit the default. Source preparation writes repository-local
`user.name` and `user.email`, so agent commands, VS Code's Git integration, and
terminals share the identity without changing global Git configuration. Reopen an
existing editor after changing the identity to refresh preparation.

Each configured checkout also receives its intended repository key and pinned
host keys in private Git metadata, with restrictive file permissions. Plain Git
commands use a repository-scoped SSH wrapper: unrelated hosts/repositories and
unrelated identities are rejected, and read-only sources cannot push. No global
`~/.ssh` installation or SSH-agent forwarding is needed. Preparation refreshes
keys and removes obsolete local credentials; forge-side key revocation remains
effective immediately at the remote provider.

The selected repository's HTTPS clone URLs also resolve to its SSH key inside
the checkout. Preparation replaces stale `origin` push addresses and overrides
host-wide SSH-to-HTTPS rewrites for that repository. Agent commands and VS Code
therefore use the same deploy key for pushes. SSH transport alone does not remove
GitHub's restrictions on a key registered through an OAuth application.

Older GitHub connections need one new device authorization to grant `workflow`:
open **Settings → Git connections → GitHub → Sign in again**, and approve workflow
access. Zentrale marks these connections in Settings. On the next project
connection or editor start, it re-registers that project's managed deploy key
with the new grant, keeping the private key and existing checkout. Interrupted
registration can be retried without creating another local key. No GitHub token
is installed in the VM. See GitHub's [OAuth scope documentation](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/scopes-for-oauth-apps).
