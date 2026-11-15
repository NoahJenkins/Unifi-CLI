# Dependency updates

## Current path

One workflow handles dependency updates:
`.github/workflows/dependabot-auto-merge.yml` enables squash auto-merge for
Dependabot patch, minor, and GitHub Actions updates. It runs on
`pull_request_target`, never checks out pull request code, and uses only
`GITHUB_TOKEN`. Major module updates get the `needs-review` label. It does not
approve pull requests. The required checks still control the merge.

Merges made with `GITHUB_TOKEN` do not start `push` workflows, so CI does not
run on `main` after a Dependabot merge. Nothing deploys on push; releases run
from tags.

Go patch releases need no pull request: `go.mod` declares the minor and
workflows use `setup-go` with `1.27.x` and `check-latest`. See
[CONTRIBUTING.md](../../CONTRIBUTING.md) for the full policy.

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

## Removed pr-repair controller

The shared `pr-repair` workflow and `.github/pr-repair.json` were removed. A
failing Dependabot pull request now stays open and visible until someone fixes
it. The `PR_REPAIR_*` repository variables and secrets, `AI_GATEWAY_API_KEY`,
`GO_TOOLCHAIN_PR_TOKEN`, and `DEPENDABOT_METADATA_TOKEN` are no longer read by
any workflow and can be deleted.
