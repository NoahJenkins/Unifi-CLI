# Copilot dependency repair

Dependency repair and independent dependency review use GitHub Copilot CLI
1.0.85 with the fixed `gpt-6-luna` model and the default context tier. There is
no automatic model/provider fallback. A new session is used for each task.

The CLI receives a fine-grained token with only the **Copilot Requests account
permission**. The token spends the owner's Copilot Pro allowance; it does not
grant repository writes. The existing dedicated App remains the only repair
publisher. Do not reuse a repository administration token for Copilot.

## Limits and credentials

- Each session has a 30 AI credit soft limit (GitHub's minimum, about $0.30).
  An in-flight response can exceed this limit. The account's zero-dollar paid
  overage budget is the separate spending stop; keep additional usage disabled.
- Repair permits at most 24 observed model calls; independent review permits
  at most 16. Wall-clock, tool-output, file-size and tool-call limits also apply.
- Existing daily and lifetime reservations remain authoritative. A failed,
  expired or ambiguous attempt stays consumed. Do not erase ledgers or replay
  claims when changing providers.
- Sanitized per-model token/request/usage counters are retained separately as
  `copilot-usage-*` artifacts for 30 days. `nanoAiu` is copied from GitHub's CLI
  usage report without a currency conversion. Missing usage is not proof of zero
  cost. The account dashboard remains the billing source of truth.

## Isolation and merge gates

The trusted CLI runs in a digest-pinned, unprivileged, read-only container.
Its only model-visible tool is `curated-read_data`: bounded list, read and literal
search of an exported input snapshot. The model has no shell, GitHub, network,
write, skill or subagent tools. No repository plugins, hooks, MCP configuration,
Git credentials or App credentials are mounted. A fresh temporary home prevents
saved sessions and local configuration from changing policy. Only the pinned CLI's
bundled native-addon cache has an executable temporary mount; data and other
temporary mounts remain non-executable.

The CLI itself needs network access to GitHub Copilot and holds the Copilot
credential. The curated tool exposes no process environment, arbitrary paths,
credentials or file writes. Repair returns proposed complete file contents as
JSON; the trusted wrapper and separate validator enforce allowed paths and scope.
Only a fresh validated result with trusted provenance can reach the publisher.
Current-head/base CI, independent review, production health, protected scope,
resolved conversations and existing branch rules still control merge.

Legacy `dependabot-opencode-*` file names, claim markers and normalized controller
transport identifiers may remain for compatibility. They do not select OpenCode.
Old OpenCode artifacts do not satisfy the new runtime identity contract.

## Setup and verification

Store `COPILOT_GITHUB_TOKEN` in the main-only `dependabot-dispatch` environment
for Portfolio, Ark, RooferChat and UniFi. Metriks uses its existing repository
Actions-secret boundary. Keep the token out of logs, issues, PRs and chat.
Do not delete the former provider secret until no separate workflow needs it.

`copilot-smoke.cjs` runs the pinned real CLI against a local fake provider in
Copilot offline mode. It verifies the exact tool allowlist and a completed real
curated search. No GitHub authentication or paid model access is used by that
check. CI runs it inside the same pinned Linux image with networking disabled.

Deployment of this configuration and activation are separate steps. Before
activation, verify the secret, model entitlement, paid-overage setting and a
controlled exact-head repair/review canary. Enable one repository first. Existing
paused flags and consumed reservations must not be reset to manufacture a pass.
