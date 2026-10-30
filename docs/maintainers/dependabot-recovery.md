# Dependency updates

## Current path

Two workflows handle dependency updates:

- `.github/workflows/dependabot-auto-merge.yml` enables squash auto-merge for
  Dependabot patch, minor, and GitHub Actions updates. A semver-major update
  auto-merges only when fetch-metadata reports an associated advisory. It does
  not approve pull requests. The required checks still control the merge.
- `.github/workflows/go-toolchain-patch.yml` opens a weekly pull request for
  the latest patch of the Go minor version in `go.mod`, then enables auto-merge
  for that pull request. It uses the helpers in `scripts/gotoolchain/`.

The advisory lookup for security-major module updates needs the Dependabot
secret `DEPENDABOT_METADATA_TOKEN` (Settings → Secrets and variables →
Dependabot, not an Actions secret). This secret is currently not set. Without
it, security-major updates stay open for manual review. See
[CONTRIBUTING.md](../../CONTRIBUTING.md) for the full policy.

## Retired recovery controller

The custom Dependabot recovery and repair controller was the earlier merge
path. Its scripts (`.github/scripts/`), agent prompts and validator image
(`.github/opencode/`), the `Copilot runtime check` workflow, and the
`Dependabot policy tests` CI job were removed in the pull request that removed
the retired Dependabot controller. No workflow ran that code after the
auto-merge workflow replaced it. Its design history stays in git history and
`docs/superpowers/`.

## Leftover GitHub configuration

The removal does not change repository settings. After the removal merges, the
owner can remove this configuration, because no workflow reads it:

- Repository variables `DEPENDABOT_AGENT_ENABLED`,
  `DEPENDABOT_AUTOMATION_ENABLED`, `DEPENDABOT_CONTROLLER_APP_ID`, and
  `DEPENDABOT_STATE_ISSUE_NUMBER`.
- Environments `dependabot-controller` and `dependabot-dispatch`. The
  `dependabot-dispatch` environment still holds the `COPILOT_GITHUB_TOKEN`
  secret. Revoke the token at its source when you delete it.
- State issue #47.
- The dedicated GitHub App installation for the controller.
- `Dependabot Merge Gate`, if it is still a required check in the main ruleset.
  Workflow changes cannot edit rulesets.
