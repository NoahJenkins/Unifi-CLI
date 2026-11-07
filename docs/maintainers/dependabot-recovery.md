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

## Shared pr-repair controller (inactive)

`.github/workflows/pr-repair.yml` calls the shared controller
`NoahJenkins/.github/pr-repair` at a pinned commit, with policy in
`.github/pr-repair.json`. It does not merge original Dependabot pull requests;
`dependabot-auto-merge.yml` keeps that role. It repairs failing Go module (patch
and minor, or security major) and Actions updates and merges only its own
validated replacement PRs, after every required check and an independent review
pass on the exact head.

Model edits are limited to `cmd/unifi`, `internal/cli`, `internal/render`,
`internal/apperr`, and `internal/fileutil`. Client, credential storage,
private files, domain, plan, live-test code, `go.mod`, and `go.sum` stay
protected, per the decision boundaries in `AGENTS.md`. Candidate checks run
`go vet ./...` and `go test ./...` in a credential-free container.

It stays inactive until all of these exist:

- a controller GitHub App: `PR_REPAIR_APP_ID` variable and
  `PR_REPAIR_APP_PRIVATE_KEY` secret;
- an explicit inference choice in `.github/pr-repair.json` (`transport`,
  `model`, `budget`, and `endpoint` for an OpenAI-compatible provider) and its
  credential as `PR_REPAIR_INFERENCE_KEY`. None is chosen, so repair and review
  report `missing-configuration` and reserve nothing;
- `PR_REPAIR_ENABLED=true`.
