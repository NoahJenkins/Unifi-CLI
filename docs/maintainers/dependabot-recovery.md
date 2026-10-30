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
Dependabot, not an Actions secret). Since 2026-10-30 it holds a fine-grained
personal access token limited to this repository with read-only Dependabot
alerts access. When the token expires or is removed, security-major updates
stay open for manual review. See [CONTRIBUTING.md](../../CONTRIBUTING.md) for
the full policy.

## Retired recovery controller

The custom Dependabot recovery and repair controller was the earlier merge
path. Its scripts (`.github/scripts/`), agent prompts and validator image
(`.github/opencode/`), the `Copilot runtime check` workflow, and the
`Dependabot policy tests` CI job were removed in #64. No workflow ran that
code after the auto-merge workflow replaced it in #53. Its design history
stays in git history and `docs/superpowers/`.

## Retired GitHub configuration

On 2026-10-30, after #64 merged, the owner removed the controller's repository
configuration: the four `DEPENDABOT_*` repository variables, the
`dependabot-controller` and `dependabot-dispatch` environments and their
secrets, state issue #47, and the dedicated GitHub App installation on this
repository. `Dependabot Merge Gate` was not a required check. The App
registration itself is not installed anywhere and can be deleted in the
owner's GitHub developer settings.
