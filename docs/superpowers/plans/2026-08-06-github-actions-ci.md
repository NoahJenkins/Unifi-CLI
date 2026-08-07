# GitHub Actions CI Implementation Plan

> **Implemented historical plan.** The workflow and three-platform checks are implemented. Required-check governance is hosted in GitHub settings and is not represented by this historical file. Use the root documentation for current contribution requirements.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run the repo's existing local gate (`scripts/smoke.sh`) in GitHub Actions on every push to `main` and every PR, across ubuntu/macos/windows, and require those checks before merging.

**Architecture:** One workflow file (`.github/workflows/ci.yml`) with a three-OS matrix job that delegates to `scripts/smoke.sh`, so local and CI run identical checks. `go vet` is added to smoke.sh so it joins the shared gate. Branch protection on `main` enforces the checks via the GitHub API.

**Tech Stack:** GitHub Actions (`actions/checkout@v4`, `actions/setup-go@v5` only), bash, `gh` CLI, Go 1.26.5.

**Spec:** `docs/superpowers/specs/2026-08-06-github-actions-ci-design.md`

## Global Constraints

- Pin Go via `go-version-file: go.mod` (do not hardcode a Go version in the workflow).
- Only third-party actions allowed: `actions/checkout@v4`, `actions/setup-go@v5`.
- No secrets, credentials, or environment variables in the workflow. The live controller suite (`UNIFI_IT=1`) stays local-only; CI runs smoke.sh without it.
- Matrix: `os: [ubuntu-latest, macos-latest, windows-latest]`, `fail-fast: false`.
- Triggers: `push` to `main`, `pull_request` to `main` only.
- Job display name must be exactly `test (${{ matrix.os }})` — branch protection depends on the resulting check names `test (ubuntu-latest)`, `test (macos-latest)`, `test (windows-latest)`.
- Commit message style: conventional commits, lowercase type prefix (repo history: `feat:`, `fix:`, `docs:`, `test:`, `chore:`).

---

### Task 1: Add vet stage to smoke.sh

**Files:**
- Modify: `scripts/smoke.sh` (lines 12–17, between build and unit tests)

**Interfaces:**
- Consumes: nothing (first task).
- Produces: `scripts/smoke.sh` runs `go build` → `go vet ./...` → `go test ./...` in order; Task 2's workflow relies on this exact sequence.

- [ ] **Step 1: Create the feature branch**

```bash
git checkout main && git pull
git checkout -b ci/github-actions
```

- [ ] **Step 2: Add the vet stage**

In `scripts/smoke.sh`, change this block:

```bash
echo "==> build"
mkdir -p "$(dirname "$BIN")"
go build -o "$BIN" ./cmd/unifi

echo "==> unit tests"
go test ./...
```

to:

```bash
echo "==> build"
mkdir -p "$(dirname "$BIN")"
go build -o "$BIN" ./cmd/unifi

echo "==> vet"
go vet ./...

echo "==> unit tests"
go test ./...
```

- [ ] **Step 3: Run the gate locally**

Run: `./scripts/smoke.sh`
Expected output, in order: `==> build`, `==> vet`, `==> unit tests`, all package results `ok`, then `==> skip live IT (set UNIFI_IT=1 to enable)`. Exit code 0.

- [ ] **Step 4: Commit**

```bash
git add scripts/smoke.sh
git commit -m "ci: add vet stage to smoke script"
```

---

### Task 2: Create the CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: `scripts/smoke.sh` from Task 1 (invoked as `bash ./scripts/smoke.sh`).
- Produces: check runs named exactly `test (ubuntu-latest)`, `test (macos-latest)`, `test (windows-latest)` on PRs and main pushes; Task 4 requires these names.

- [ ] **Step 1: Write the workflow file**

Create `.github/workflows/ci.yml` with exactly:

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true

jobs:
  test:
    name: test (${{ matrix.os }})
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: smoke (build + vet + unit tests)
        shell: bash
        run: bash ./scripts/smoke.sh
```

Notes:
- `shell: bash` is required so the Windows runner uses Git Bash for the run step.
- Strict YAML 1.1 parsers read the key `on:` as boolean `true`; that is expected and harmless — GitHub's parser handles it correctly.

- [ ] **Step 2: Syntax-check the YAML**

Run: `ruby -ryaml -e 'YAML.load_file(".github/workflows/ci.yml"); puts "yaml ok"'`
Expected: `yaml ok` (ruby ships with macOS).

- [ ] **Step 3: Commit and push the branch**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add GitHub Actions test workflow"
git push -u origin ci/github-actions
```

---

### Task 3: Open the PR and verify the matrix is green

**Files:** none (verification only, unless the contingency in Task 4 triggers).

**Interfaces:**
- Consumes: pushed branch from Task 2.
- Produces: a PR with three green check runs, proving the check names Task 5 will require.

- [ ] **Step 1: Open the PR**

```bash
gh pr create --title "ci: add GitHub Actions test workflow" --body "Adds a three-OS CI matrix (ubuntu/macos/windows) running scripts/smoke.sh — build, vet, and unit tests — on pushes to main and PRs. Vet is added to smoke.sh so local and CI run the identical gate. Design: docs/superpowers/specs/2026-08-06-github-actions-ci-design.md"
```

- [ ] **Step 2: Watch the checks**

Run: `gh pr checks --watch`
Expected: exactly three checks, all passing:
- `test (ubuntu-latest)` — pass
- `test (macos-latest)` — pass
- `test (windows-latest)` — pass

- [ ] **Step 3: Triage on failure**

If a check fails, read the log before touching anything:

```bash
gh run view --log-failed
```

- If `test (ubuntu-latest)` fails inside `internal/authstore` with keyring/dbus/Secret Service errors → apply Task 4 (conditional contingency), then return here.
- Any other failure → stop and diagnose; do not merge a red PR.

---

### Task 4 (conditional): Mock the keyring for headless Linux

**Only do this task if Task 3 Step 3 routed here.** Headless Linux runners have no Secret Service provider, so real keyring calls fail. `keyring` is only used by `internal/authstore/store.go`, so this is the only package affected.

**Files:**
- Modify: `internal/authstore/store_test.go` (imports and top of file)

**Interfaces:**
- Consumes: `github.com/zalando/go-keyring` (already a direct dependency in go.mod; provides `keyring.MockInit()` and `keyring.MockInitWithError(err)`).
- Produces: `internal/authstore` tests that pass without a native credential store.

- [ ] **Step 1: Add a TestMain with the in-memory keyring mock**

In `internal/authstore/store_test.go`, add `os` (if not already imported) and `github.com/zalando/go-keyring` to the import block, then add immediately after the imports:

```go
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}
```

- [ ] **Step 2: Run the package tests locally**

Run: `go test ./internal/authstore/ -v`
Expected: all tests PASS.

If a specific test now fails because it exercises keyring-*error* behavior (e.g. fallback-on-error paths), replace the global mock for just that test by calling at its start:

```go
keyring.MockInitWithError(errors.New("keyring unavailable"))
```

(add `errors` to imports if needed) — do not change assertions to make a test pass.

- [ ] **Step 3: Commit, push, re-verify**

```bash
git add internal/authstore/store_test.go
git commit -m "test: mock keyring in authstore tests"
git push
gh pr checks --watch
```

Expected: all three `test (...)` checks pass.

---

### Task 5: Require the checks on main (branch protection)

**Files:** none (GitHub API only).

**Interfaces:**
- Consumes: the exact check names proven green in Task 3.
- Produces: `main` branch protection requiring the three matrix checks.

- [ ] **Step 1: Apply branch protection**

```bash
gh api repos/NoahJenkins/Unifi-CLI/branches/main/protection --method PUT --input - <<'JSON'
{
  "required_status_checks": {
    "strict": false,
    "contexts": ["test (ubuntu-latest)", "test (macos-latest)", "test (windows-latest)"]
  },
  "enforce_admins": false,
  "required_pull_request_reviews": null,
  "restrictions": null
}
JSON
```

Expected: JSON response echoing the protection settings, no error.

- [ ] **Step 2: Verify**

Run: `gh api repos/NoahJenkins/Unifi-CLI/branches/main/protection --jq '.required_status_checks.contexts'`
Expected output (order may vary):

```json
[
  "test (ubuntu-latest)",
  "test (macos-latest)",
  "test (windows-latest)"
]
```

---

### Task 6: Merge and verify main stays green

**Files:** none.

**Interfaces:**
- Consumes: green PR (Tasks 3–4) and branch protection (Task 5).
- Produces: merged `main` with a passing CI run, completing the spec's verification criteria.

- [ ] **Step 1: Merge**

```bash
gh pr merge --rebase --delete-branch
```

(`--rebase` keeps the repo's linear history and preserves both task commits. If GitHub rejects it because rebase merging is disabled on the repo, use `gh pr merge --merge --delete-branch` instead.)

- [ ] **Step 2: Sync local main and confirm the main-branch run**

```bash
git checkout main && git pull
gh run list --branch main --limit 1
```

Expected: the newest run on `main` shows workflow `CI`, status `completed`, conclusion `success`.

---

## Self-Review Notes

- **Spec coverage:** workflow (Task 2), smoke.sh vet stage (Task 1), triggers/matrix/concurrency/go-version-file constraints (Task 2 YAML, Global Constraints), PR-as-first-run verification (Task 3), known-risk keyring contingency (Task 4), branch protection via API (Task 5), merge + main green (Task 6). Every spec section maps to a task.
- **Check-name consistency:** `name: test (${{ matrix.os }})` (Task 2) → `test (ubuntu-latest)` etc. asserted in Task 3 Step 2, required in Task 5, re-verified in Task 6. Names match everywhere.
- **No placeholders:** conditional Task 4 is fully specified with exact code; it is skipped, not partially defined, when unnecessary.
