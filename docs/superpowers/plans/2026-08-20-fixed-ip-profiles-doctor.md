# Fixed-IP Inventory, Controller Profiles, and Doctor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reconcile and update the local CLI, add complete fixed-IP inventory and safe offline targeting, add named non-secret controller profiles, and add local readiness diagnostics.

**Architecture:** Legacy fixed-IP user records remain isolated in `internal/domain` and keep the existing prepared-mutation gates. Profile selection is a local configuration concern in `internal/config`, while Cobra commands only render and invoke it. Doctor composes build metadata, resolved configuration, TLS mode, and credential-store presence without creating an HTTP client or sending a request.

**Tech Stack:** Go 1.26.6 or newer compatible toolchain, Cobra, YAML v3, existing `authstore`, `privatefile`, `render`, and `plan` packages.

**Spec:** `docs/superpowers/specs/2026-08-20-fixed-ip-profiles-doctor-design.md`

## Global Constraints

- Stable commands continue to use only the official local integration API.
- Fixed-IP behavior remains an experimental legacy compatibility surface.
- No write applies without `--yes`; `--dry-run` always wins.
- Fixed-IP applies require `--experimental --force --yes` while `safe_mode` is enabled.
- API keys never enter configuration, output, tests, logs, errors, plans, or command arguments.
- Preserve the old Keychain item; do not delete, migrate, rewrite, or print it.
- Do not perform a live controller write.
- Do not commit, push, open a pull request, publish, or release.

---

### Task 1: Reconcile the checkout, install v1.0.0, and create isolation

**Files:**
- Preserve untracked spec and plan documents in the feature worktree.
- No production source changes.

**Interfaces:**
- Consumes: local `main`, `origin/main`, and the installed Go-path `unifi` binary.
- Produces: reconciled `main`, backup branch `codex/pre-reconcile-main-20260820`, worktree branch `codex/fixed-ip-profiles-doctor`, verified v1.0.0 executable.

- [x] **Step 1: Fetch and verify the exact remote state**

```bash
git fetch origin
git status --short --branch
git rev-parse HEAD origin/main
git log --left-right --cherry-pick --oneline HEAD...origin/main
```

Expected: the existing local fixed-IP commit is patch-equivalent to work already present on `origin/main`; no tracked working-tree changes exist.

- [x] **Step 2: Preserve and reconcile local main**

```bash
git branch codex/pre-reconcile-main-20260820 HEAD
git rebase origin/main
git status --short --branch
```

Expected: the backup retains the old head and `main` points at or cleanly rebases onto current `origin/main` without lost files.

- [x] **Step 3: Install and verify stable v1.0.0**

```bash
go install github.com/noahjenkins/unifi-cli/cmd/unifi@v1.0.0
~/go/bin/unifi --version
~/go/bin/unifi version --json
~/go/bin/unifi client fixed-ip --help
```

Expected: version `1.0.0`; help exposes `set` and `clear` and no retired session flags.

- [x] **Step 4: Create the isolated worktree**

```bash
git check-ignore -q .worktrees
git worktree add .worktrees/fixed-ip-profiles-doctor -b codex/fixed-ip-profiles-doctor origin/main
go mod download
go test ./...
```

Expected: clean baseline tests in the new worktree. Recreate the approved spec and this plan there with `apply_patch` so they travel with the implementation.

### Task 2: Add fixed-IP inventory and legacy-user resolution

**Files:**
- Modify: `internal/domain/client_fixed_ip.go`
- Modify: `internal/domain/client_fixed_ip_test.go`

**Interfaces:**
- Consumes: `ClientAPI`, `ClientFixedIPReservation`, legacy `rest/user` and `rest/networkconf` routes.
- Produces: `(*ClientFixedIPService).List(context.Context) ([]ClientFixedIPReservation, error)`, `Get(context.Context, string) (ClientFixedIPReservation, error)`, and a private exact user resolver reused by `prepare`.

- [x] **Step 1: Write failing inventory tests**

Add tests whose wished-for API is:

```go
items, err := domain.NewClientFixedIPService(api).List(context.Background())
got, err := domain.NewClientFixedIPService(api).Get(context.Background(), "Printer")
```

Assert that list returns only enabled reservations in deterministic name/MAC/ID order; get returns disabled state with an empty effective `fixed_ip`; exact ID and normalized MAC work; missing and duplicate names fail with the established `not_found` and `ambiguous_id` codes.

- [x] **Step 2: Verify the inventory tests fail for the missing methods**

```bash
go test ./internal/domain -run 'TestClientFixedIP(List|Get)' -count=1
```

Expected: compile failure because `List` and `Get` do not exist.

- [x] **Step 3: Implement minimal normalized inventory**

Use one bounded legacy collection read and map each row through a helper shaped as:

```go
func reservationFromUser(user map[string]any) ClientFixedIPReservation
func (s *ClientFixedIPService) listUsers(ctx context.Context) ([]map[string]any, error)
func (s *ClientFixedIPService) resolveUser(ctx context.Context, query string) (map[string]any, error)
```

Normalize MACs, choose name then hostname, suppress inactive historical `fixed_ip`, and reuse `resolve.One` semantics through a small typed record implementing `GetID`, `GetMAC`, and `GetName`.

- [x] **Step 4: Verify inventory tests pass**

```bash
go test ./internal/domain -run 'TestClientFixedIP(List|Get)' -count=1
```

Expected: PASS.

- [x] **Step 5: Write failing offline planning tests**

Remove the target from `stat/sta` while retaining a valid legacy user and network. Assert:

```go
p, snapshot, err := svc.Set(ctx, "Offline Laptop", "192.0.2.50")
p, snapshot, err := svc.Clear(ctx, "Offline Laptop")
```

produce an immutable legacy ID/MAC snapshot, and assert missing network ID, invalid MAC, or ambiguous user matches fail before PUT.

- [x] **Step 6: Verify offline tests fail because prepare still requires an active station**

```bash
go test ./internal/domain -run 'TestClientFixedIPOffline' -count=1
```

Expected: FAIL with client not found from the active-station lookup.

- [x] **Step 7: Change prepare to use the resolved legacy user**

Build `ClientFixedIPSnapshot` directly from `_id`, normalized `mac`, `name` or `hostname`, `network_id` or `networkconf_id`, `use_fixedip`, and `fixed_ip`. Resolve the network only after identity checks. Keep validation, prepared-target comparison, PUT payloads, post-write reads, and no-retry behavior unchanged.

- [x] **Step 8: Run all fixed-IP domain tests**

```bash
go test ./internal/domain -run ClientFixedIP -count=1
```

Expected: PASS with no unexpected HTTP request.

### Task 3: Expose fixed-IP list/get commands

**Files:**
- Modify: `internal/cli/clientcmd.go`
- Modify: `internal/cli/client_fixed_ip_cmd_test.go`
- Modify: `internal/cli/context.go`

**Interfaces:**
- Consumes: domain `List` and `Get` from Task 2.
- Produces: `client fixed-ip list`, `client fixed-ip get <client>`, deterministic human tables, and schema-v1 actions `fixed-ip list` and `fixed-ip get`.

- [x] **Step 1: Write failing command discovery and output tests**

Extend command discovery to require:

```go
[][]string{{"fixed-ip", "list"}, {"fixed-ip", "get"}, {"fixed-ip", "set"}, {"fixed-ip", "clear"}}
```

Add exact JSON assertions for enabled-only list and disabled get. Add a human table assertion with columns `NAME`, `MAC`, `FIXED IP`, and `NETWORK ID`. Assert no API key or inactive stored address appears.

- [x] **Step 2: Verify command tests fail**

```bash
go test ./internal/cli -run 'TestClientFixedIP(Commands|List|Get)' -count=1
```

Expected: FAIL because list/get subcommands are absent.

- [x] **Step 3: Implement read commands and rendering**

Add `newClientFixedIPListCmd`, `newClientFixedIPGetCmd`, `runClientFixedIPList`, and `runClientFixedIPGet`. Mark successful read envelopes as experimental legacy metadata without requiring `--experimental`, consistent with plan-only behavior. Keep mutation routing unchanged.

- [x] **Step 4: Verify fixed-IP CLI tests pass**

```bash
go test ./internal/cli -run ClientFixedIP -count=1
```

Expected: PASS.

### Task 4: Add profile storage and selection precedence

**Files:**
- Create: `internal/config/profile.go`
- Create: `internal/config/profile_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/helpers.go`
- Modify: `internal/cli/context.go`

**Interfaces:**
- Consumes: existing strict `config.Load`, `fileutil`, and `privatefile` helpers.
- Produces: `config.ProfileStore`, `config.Selection`, `config.ResolveSelection`, global `--profile`, and `UNIFI_PROFILE` support.

- [x] **Step 1: Write failing profile-name and store tests**

Define the intended types in tests:

```go
type Selection struct { Profile string; Path string }
type ProfileInfo struct { Name, Path string; Selected, Valid bool; Error string }
store := config.NewProfileStore(config.ProfileOptions{ConfigHome: dir})
```

Test valid names, rejected traversal/whitespace, sorted list output, regular-file enforcement, symlink rejection, missing marker, malformed marker, and atomic select that validates the profile before changing the marker.

- [x] **Step 2: Verify profile store tests fail to compile**

```bash
go test ./internal/config -run Profile -count=1
```

Expected: compile failure because profile types do not exist.

- [x] **Step 3: Implement the profile store**

Implement:

```go
func NewProfileStore(ProfileOptions) *ProfileStore
func ValidateProfileName(string) error
func (s *ProfileStore) List() ([]ProfileInfo, error)
func (s *ProfileStore) Show(string) (ProfileInfo, Config, error)
func (s *ProfileStore) Selected() (string, bool, error)
func (s *ProfileStore) Select(string) error
func ResolveSelection(explicitConfig, explicitProfile string, store *ProfileStore) (Selection, error)
```

Use bounded regular-file reads, `0700` directories, `0600` marker files, temporary-file sync and rename, and the exact precedence in the spec. Reject simultaneous explicit config and profile selectors.

- [x] **Step 4: Verify config profile tests pass**

```bash
go test ./internal/config -run Profile -count=1
```

Expected: PASS.

- [x] **Step 5: Write failing runtime-precedence tests**

Test `--config`, `UNIFI_CONFIG`, `--profile`, `UNIFI_PROFILE`, selected marker, and default config in order. Assert `Runtime` records `ConfigPath` and `Profile`, and explicit config plus explicit profile fails with `validation_failed`.

- [x] **Step 6: Verify runtime tests fail, then wire selection into loadRuntime**

```bash
go test ./internal/cli -run 'TestRuntime(Profile|ConfigSelection)' -count=1
```

Add `flagProfile`, extend `Runtime` with `ConfigPath` and `Profile`, resolve the file before `config.Load`, and preserve site/timeout overrides.

- [x] **Step 7: Verify runtime selection tests pass**

```bash
go test ./internal/cli -run 'TestRuntime(Profile|ConfigSelection)' -count=1
```

Expected: PASS.

### Task 5: Add profile commands and effective config reporting

**Files:**
- Create: `internal/cli/profilecmd.go`
- Create: `internal/cli/profilecmd_test.go`
- Modify: `internal/cli/configcmd.go`
- Modify: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: `ProfileStore`, `Selection`, and runtime fields from Task 4.
- Produces: `config profile list`, `show [name]`, `select <name>`, profile-aware `config path`, and profile-aware `config show`.

- [x] **Step 1: Write failing profile command tests**

Use an injected temporary `ProfileStore`. Assert sorted list JSON includes name, path, validity, error, and selection; show emits normalized non-secret fields; select updates only the marker; invalid profiles do not block valid list entries; secret field names and values never appear.

- [x] **Step 2: Verify command tests fail**

```bash
go test ./internal/cli -run 'TestConfigProfile|TestConfig(Path|Show).*Profile' -count=1
```

Expected: FAIL because profile commands are absent.

- [x] **Step 3: Implement profile commands**

Add the profile group under `config`. `list` and `show` are local reads. `select` validates and atomically writes the marker. Route output through `Runtime.Emit` with actions `profile list`, `profile show`, and `profile select`.

- [x] **Step 4: Update config path/show**

`config path` reports the effective resolved file, not always `DefaultPath`. `publicConfig` adds `profile`, `path`, and `ca_cert` without credential fields. Preserve deterministic field order in `context.go`.

- [x] **Step 5: Verify profile CLI tests pass**

```bash
go test ./internal/cli -run 'TestConfigProfile|TestConfig(Path|Show)' -count=1
```

Expected: PASS.

### Task 6: Add local-only doctor diagnostics

**Files:**
- Create: `internal/cli/doctor.go`
- Create: `internal/cli/doctor_test.go`
- Modify: `internal/cli/root.go`
- Modify: `internal/authstore/store.go` only if a safe presence-status interface is required.

**Interfaces:**
- Consumes: build metadata, `ResolveSelection`, `config.Load`, `authstore.Store.Load`, and `UNIFI_API_KEY` presence.
- Produces: `unifi doctor`, `DoctorResult`, deterministic human output, schema-v1 JSON, and no HTTP request.

- [x] **Step 1: Write failing doctor tests**

Use injected build info, profile store, and auth store. Assert the exact data shape:

```go
type DoctorResult struct {
    Version, Commit, ConfigPath, Profile, Host, Site string
    TLSMode, CredentialSource string
    Ready bool
}
```

Cover system roots, custom CA, insecure mode, environment key, saved key,
missing key, unavailable keyring, invalid configuration, redaction, and a test
HTTP transport that panics if any request occurs.

- [x] **Step 2: Verify doctor tests fail**

```bash
go test ./internal/cli -run Doctor -count=1
```

Expected: compile or command-discovery failure because doctor is absent.

- [x] **Step 3: Implement doctor without constructing a client**

Resolve and load configuration locally. Determine TLS mode from `CACert` and
`Insecure`. If `UNIFI_API_KEY` is nonempty, report `environment_api_key` without
reading its value. Otherwise call `Store.Load(cfg.BaseURL())`, discard the
returned key immediately, and report only `saved_api_key`, `missing`, or
`keyring_unavailable`. Do not call `client.New`, `Validate`, or any HTTP method.

- [x] **Step 4: Verify doctor tests pass**

```bash
go test ./internal/cli -run Doctor -count=1
```

Expected: PASS with no secret in stdout, stderr, or errors.

### Task 7: Update public contracts and create the Home Lab profile

**Files:**
- Modify: `README.md`
- Modify: `docs/compatibility.md`
- Modify: `CHANGELOG.md`
- Create outside repository after discovery: `~/.config/unifi-cli/profiles/home-lab.yaml`

**Interfaces:**
- Consumes: final command behavior from Tasks 2–6.
- Produces: accurate public usage, documented proof limits, selected non-secret local profile.

- [x] **Step 1: Update documentation**

Document all four fixed-IP commands, enabled-only list behavior, disabled get
behavior, offline identity/network requirements, incomplete detection of
unrecorded offline static IPs, profile precedence and files, profile commands,
doctor fields, and the separation between doctor and online `auth status`.

- [x] **Step 2: Run documentation and help consistency checks**

```bash
rg -n 'client fixed-ip (list|get|set|clear)|config profile (list|show|select)|unifi doctor' README.md docs/compatibility.md CHANGELOG.md
go run ./cmd/unifi --help
go run ./cmd/unifi client fixed-ip --help
go run ./cmd/unifi config profile --help
```

Expected: public commands and limits agree with help.

- [x] **Step 3: Discover and validate the Home Lab controller host read-only**

```bash
route -n get default
```

Confirm UniFi identity through existing local evidence or a bounded read-only
HTTPS check before using the gateway address. Do not print credentials or a
controller payload.

- [x] **Step 4: Create and select the profile**

Use `apply_patch` to create a non-secret `home-lab.yaml` with mode `0600`, then:

```bash
unifi config profile select home-lab
$ unifi doctor --json
```

Expected: configuration and profile fields pass. If the saved v1 key is absent,
doctor reports the credential prerequisite without altering the old Keychain
item.

### Task 8: Run repository gates and inspect final state

**Files:**
- All changed files from Tasks 2–7.

**Interfaces:**
- Consumes: complete implementation.
- Produces: fresh verification evidence and a clean, reviewable working diff.

- [x] **Step 1: Format and run focused tests**

```bash
gofmt -w internal/config/profile.go internal/config/profile_test.go internal/cli/profilecmd.go internal/cli/profilecmd_test.go internal/cli/doctor.go internal/cli/doctor_test.go internal/cli/clientcmd.go internal/cli/client_fixed_ip_cmd_test.go internal/domain/client_fixed_ip.go internal/domain/client_fixed_ip_test.go
go test ./internal/config ./internal/domain ./internal/cli
```

Expected: PASS.

- [x] **Step 2: Run required gates**

```bash
./scripts/smoke.sh
go test -race ./...
./scripts/check-coverage.sh
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
```

Expected: all gates pass without weakening tests.

- [x] **Step 3: Inspect the final diff and installed state**

```bash
git status --short --branch
git diff --check
git diff --stat
~/go/bin/unifi --version
```

Expected: only approved source, test, and documentation files are changed in
the feature worktree; stable installed binary remains v1.0.0; no commit, push,
release, or controller write occurred.
