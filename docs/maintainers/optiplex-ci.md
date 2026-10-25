# Prepared Linux x64 CI routing

This phase changes only the CI workflow. It prepares repository-scoped
disposable Ubuntu 24.04 x64 workers labelled exactly `optiplex-ci-ubuntu24`;
it does not activate the pool or qualify Dell execution.

## Routing and check identity

Only the exact repository variable value `OPTIPLEX_CI_ENABLED=true` opts in.
Missing, false, differently cased, and other values retain hosted execution.
PRs qualify only when their head repository is the current repository;
actor names, including Dependabot, do not establish trust. Missing PR head
repositories and unknown event types stay hosted.

[GitHub expression comparisons ignore case](https://docs.github.com/en/actions/reference/workflows-and-actions/expressions).
The new **select Linux CI runner** job therefore runs a case-sensitive Bash
selector on `ubuntu-latest`, without checking out or running PR code. Its
output is one of two constant labels. The four scoped jobs now depend on
this selector. This hosted job, dependency, and status condition are the
scheduling compatibility adjustment. Its timeout is five minutes. Missing
selector output falls back to hosted runners; `!cancelled()` lets required
checks execute after selector failure while honoring workflow cancellation.
This avoids [required checks being accepted when skipped after a failed dependency](https://docs.github.com/en/pull-requests/how-tos/merge-and-close-pull-requests/troubleshooting-required-status-checks).

`matrix.os` retains its original values. An `include` entry adds a scheduling
`runner` only for the Linux leg; macOS and Windows fall back to `matrix.os`.
Existing names, required contexts, runner-OS conditions, triggers, permissions,
action pins, timeouts, cancellation, tests, and artifacts remain unchanged.

| Job/check | Before | Opted-in same-repository after |
| --- | --- | --- |
| test (ubuntu-latest) | ubuntu-latest | optiplex-ci-ubuntu24 |
| test (macos-latest) | macos-latest | macos-latest |
| test (windows-latest) | windows-latest | windows-latest |
| govulncheck | ubuntu-latest | optiplex-ci-ubuntu24 |
| fuzz (FuzzStrictYAMLConfiguration) | ubuntu-latest | optiplex-ci-ubuntu24 |
| fuzz (FuzzOfficialPaginationJSON) | ubuntu-latest | optiplex-ci-ubuntu24 |
| fuzz (FuzzOfficialPathAndFilterEscaping) | ubuntu-latest | optiplex-ci-ubuntu24 |
| fuzz (FuzzReleaseBundleExtraction) | ubuntu-latest | optiplex-ci-ubuntu24 |
| fuzz (FuzzChecksumManifest) | ubuntu-latest | optiplex-ci-ubuntu24 |
| fuzz (FuzzSBOMSnapshotParsing) | ubuntu-latest | optiplex-ci-ubuntu24 |
| native keyring (ubuntu-latest) | ubuntu-latest | optiplex-ci-ubuntu24 |
| native keyring (macos-latest) | macos-latest | macos-latest |
| native keyring (windows-latest) | windows-latest | windows-latest |

Every Linux row retains `ubuntu-latest` for forks or without exact opt-in.
The new selector always uses `ubuntu-latest`. Neither `optiplex-ci-ubuntu22`
nor either Tailscale policy label is used. No checks are removed to avoid queues.

## Infrastructure image requirements

- Reviewed Ubuntu 24.04 **x86_64** image, fresh per job. The controller caps
  the entire pool at one active job, at most 4 vCPUs and 16 GiB RAM.
- Supported Actions runner with Node 24 action support (at least 2.327.1
  for the pinned checkout). Install reviewed runner runtime dependencies;
  Ubuntu 24.04 candidates include `libkrb5-3`, `zlib1g`, `liblttng-ust1t64`,
  `libssl3t64`, and `libicu74`. See the [runner dependency installer](https://github.com/actions/runner/blob/main/src/Misc/layoutbin/installdependencies.sh)
  and [pinned checkout requirements](https://github.com/actions/checkout/blob/3d3c42e5aac5ba805825da76410c181273ba90b1/README.md).
- `bash`, `git`, `ca-certificates`, `curl`, `tar`, `gzip`, `unzip`, `coreutils`,
  `sudo`, and `build-essential` (the race detector needs a C compiler).
  The existing package step requires working `apt-get` and noninteractive sudo.
- **`dbus`, `dbus-x11`, and `gnome-keyring`**, including `dbus-run-session`
  and `gnome-keyring-daemon`. No desktop is needed. Preserve the isolated
  D-Bus shell and unlocked temporary login collection; native tests must run.
- Go version from `go.mod`, via pinned setup-go; govulncheck **v1.6.0**.
  Preserve checkout **v7.0.1** and setup-go **v7.0.0** immutable pins.
- HTTPS for GitHub checkout/actions/caches, Go modules/toolchains, the Go
  vulnerability database, and approved Ubuntu repositories. Supply writable
  job-local HOME, workspace, temp, Go caches, and private keyring directories.

Destroy writable disks after success, failure, cancellation, or timeout.
Do not share writable HOME/workspace or mount the host Docker socket, host
SSH credentials, home directories, production data, or home-network access.
Docker, if provided, runs inside the disposable VM; these jobs do not require
it. Lifecycle isolation and cleanup belong to the infrastructure controller.

## Validation and live acceptance

`TestCIRunnerRouting` executes the workflow's actual Bash selector with
synthetic inputs and follows its output through the real scheduling matrices.
It covers enabled, disabled, absent, forks, Dependabot forks, malformed values,
and case-sensitive opt-in. Selector-failure coverage protects hosted fallback
and required-check execution. `TestCICheckContextsRemainStable` protects names.
Run repository gates with the Go version in `go.mod` and validate all workflows with actionlint.
Existing tests still protect race mode, native keyring setup, seals and provenance.

On the approved Ubuntu 24.04 x64 canary, execute the Linux Secret Service
step's exact `dbus-run-session` shell/script and all six matrix commands:
`go test <package> -run '^$' -fuzz '^<target>$' -fuzztime=30s`.
Also run Linux race smoke, coverage, native release executable, cross-build
inspection, and pinned govulncheck. Use synthetic credential-store entries;
do not configure or contact a UniFi controller.

Local macOS and hosted checks do not prove Dell execution. Live acceptance
requires infrastructure acceptance and a complete successful CI workflow at
the exact PR head, expected Dell runners for every opted-in Linux job, ordinary
hosted fork routing, unchanged artifacts, and controller evidence of writable
disk destruction for every termination path. Record run URLs, exact head SHA,
runner labels, and cleanup evidence. CI has no artifact-upload steps; release
artifact validation remains hosted. Do not dispatch publication/release tests.

## Work remaining hosted

All of `release.yml` remains unchanged and hosted:

| Job | Current selector | Follow-up scope |
| --- | --- | --- |
| preflight | ubuntu-latest | Keep tag/publication preflight hosted |
| trusted_inputs | ubuntu-latest | Potential ordinary Linux all-target build/source-manifest work |
| generate | ubuntu-latest | Potential ordinary Linux GoReleaser/SBOM generation work |
| verify | ubuntu-latest | Potential ordinary Linux seal/extraction/artifact comparison work |
| smoke_artifacts | ubuntu-24.04, ubuntu-24.04-arm, macos-15-intel, macos-15, windows-2025, windows-11-arm | Potential Linux x64 smoke only; retain native ARM64/macOS/Windows coverage |
| attest | ubuntu-latest | Keep signing/attestation and source-bound hosted provenance unchanged |
| prepare_publication | ubuntu-latest | Keep publication bundle sealing hosted |
| publish | ubuntu-latest | Keep release publication hosted |

Any later build/verify migration needs separate review of artifact seals,
trusted inputs, and provenance; never silently change the attestation's
`github-hosted` claim. Pinned release tools remain outside this change.

`dependabot-auto-merge.yml`'s **Enable auto-merge for safe dependency updates**
remains on `ubuntu-latest`. `go-toolchain-patch.yml` also stays on
`ubuntu-latest`. GitHub-managed Dependabot, CodeQL default setup,
and Copilot workflows are outside this YAML migration. Production deployment/
migration and provider-secret agent repair/review are outside this repository
CI scope and must stay on their existing hosted runners.

## Rollback

An authorized maintainer can unset `OPTIPLEX_CI_ENABLED` or set it to `false`
to restore original hosted selectors for subsequent runs. Already queued or
running jobs do not move; cancel and rerun affected CI after the variable
change, obtaining disk-destruction evidence from infrastructure. For code
rollback, revert the migration commit and rerun CI. Preserve required checks,
release provenance, runner labels, and the Tailscale pool.
