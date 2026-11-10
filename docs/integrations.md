# Model and Git connections

[← Documentation](README.md)

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
| Google / Gemini | API key                                            |
| Mistral         | API key                                            |
| OpenCode        | API key                                            |
| LiteLLM         | Configurable API endpoint, optional key            |

Model lists come from the connected account. Refresh or search them in the UI.
For OpenAI device login, device authentication must be enabled by the ChatGPT
account or workspace. Zentrale uses native
[OpenAI login](https://developers.openai.com/codex/auth) and
[OpenRouter PKCE](https://openrouter.ai/docs/guides/overview/auth/oauth), without
importing local CLI sessions.

Missing context sizes use a marked 32,000-token estimate. Context sizes and
prices can be edited from the model's menu; overrides survive catalog refreshes.
Prices are USD per million tokens. Usage separates metered costs from
API-equivalent costs for fixed-price accounts and marks estimated or missing
token and cache data.

## Connect GitHub or GitLab

Git connections use device login, available in Settings or directly in the
project/source repository picker. Register your own OAuth application first:

- **GitHub:** enable Device Flow and provide the client ID. Requested scopes are
  `repo read:org workflow`. The additional `workflow` grant allows changes to
  GitHub Actions workflow files, including pushes using OAuth-created deploy keys.
- **GitLab:** provide a client ID, `api` scope, and a client secret for confidential
  applications. The instance must support Device Flow. Self-hosted HTTPS
  instances can be configured.

Enter app configuration in the UI or through `GITHUB_CLIENT_ID`,
`GITLAB_CLIENT_ID`, optional `GITHUB_CLIENT_SECRET` / `GITLAB_CLIENT_SECRET`, and
`GITLAB_BASE_URL`. Tokens remain in the backend. One account can be connected per
service. See the application setup documentation for
[GitHub](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps#device-flow)
and [GitLab](https://docs.gitlab.com/api/oauth2/#device-authorization-grant-flow).

## Repositories and keys

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
and scopes them to GitHub's SSH host; other Git hosts use explicitly trusted keys.

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
