# GitHub Actions CI Design

**Date:** 2026-08-06  
**Status:** Approved for implementation planning  
**Scope:** Automated build, vet, and unit-test checks on GitHub Actions

## Goal

Run the repository's existing local gate (`scripts/smoke.sh`) automatically on
every push to `main` and every pull request targeting `main`, across the three
operating systems the CLI supports, and require those checks to pass before
merging. The live controller test suite stays local-only; CI never needs a
UniFi controller, credentials, or secrets.

## Decisions

| Question | Decision |
|---|---|
| CI scope | Build + `go vet` + `go test ./...` (standard) |
| Platforms | `ubuntu-latest`, `macos-latest`, `windows-latest` matrix |
| Triggers | Push to `main` + PRs targeting `main` |
| Entrypoint | CI reuses `scripts/smoke.sh`; no duplicated go commands in YAML |
| Enforcement | Branch protection on `main` requiring the three matrix checks |

The OS matrix is justified by the credential-store code: `go-keyring` backs
onto Keychain (macOS), wincred (Windows), and Secret Service/dbus (Linux), so
build and test behavior genuinely differs per platform.

## Workflow

Single file `.github/workflows/ci.yml`:

- **Triggers:** `push` on branch `main`; `pull_request` on branch `main`.
- **Concurrency:** one group per workflow+ref with `cancel-in-progress: true`
  so superseded runs (e.g. force-pushes) stop early.
- **Job `test`:** matrix over the three OSes with `fail-fast: false` so one
  platform's failure does not hide the others.
- **Steps:**
  1. `actions/checkout`
  2. `actions/setup-go` with `go-version-file: go.mod` — pins the Go version
     to the one the repo declares (currently 1.26.5) and enables module
     caching automatically.
  3. `bash ./scripts/smoke.sh` with `shell: bash` so the same script runs on
     Windows runners via Git Bash.

No third-party actions beyond checkout and setup-go. No secrets, no
environment variables, no services.

## smoke.sh change

Insert a vet stage between build and unit tests:

```
==> build
==> vet        (new: go vet ./...)
==> unit tests
```

Vet becomes part of the local gate too, so local and CI run identical checks
(the reason for reusing smoke.sh rather than inlining go commands in YAML).

## Branch protection

After the workflow has run at least once (so the check names exist), require
the three matrix checks on `main` via the GitHub API:

- `test (ubuntu-latest)`
- `test (macos-latest)`
- `test (windows-latest)`

Otherwise-default settings: no required reviews, no up-to-date-branch
requirement, admins not restricted.

## Known risk

`internal/authstore` tests pass on macOS locally, but headless Linux runners
have no Secret Service provider. If `ubuntu-latest` fails on keyring calls,
the fix is `keyring.MockInit()` in the affected tests. This is handled only if
CI actually fails — no speculative changes.

## Verification

The change is implemented on a feature branch and opened as a PR, which is
itself the first CI run. The work is complete when:

- the PR shows all three matrix checks green;
- branch protection is active on `main` (verified with `gh`);
- merging the PR leaves `main` green.
