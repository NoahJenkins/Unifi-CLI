# API-Key-Only Persistent Authentication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace password and cookie-session authentication with persistent, controller-scoped API-key authentication that supports interactive login and non-interactive automation.

**Architecture:** `internal/authstore` replaces the session package and owns serialized API-key records in the native credential store plus an explicit protected-file fallback. `internal/client` resolves either `UNIFI_API_KEY` or the saved key and sends only `X-API-KEY`; it removes a failed saved key on `401`. The CLI provides top-level `login` and `logout` commands and a hidden TTY prompt, while configuration remains non-secret.

**Tech Stack:** Go 1.26.5, Cobra, `github.com/zalando/go-keyring`, `golang.org/x/term`, `gopkg.in/yaml.v3`, Go `net/http`.

## Global Constraints

- Authentication is API-key-only; no password request, cookie restoration, CSRF handling, or legacy session-auth path remains usable.
- Persist a key only after a successful read-only validation request.
- Resolve credentials in this exact order: `UNIFI_API_KEY`, saved controller-scoped key, then `not_authenticated` with a `unifi login` hint.
- Never print an API key, include one in normal config, accept one in a flag or positional argument, or expose it in an error, JSON envelope, or log.
- Save state in macOS Keychain, Windows Credential Manager, or Linux Secret Service. Permit a protected-file fallback only when `unifi login --file-fallback` is explicitly supplied.
- Scope saved state by normalized scheme, host, and port. An environment override never writes or deletes saved state.
- Existing `username`, `password`, and YAML `api_key` settings must fail with migration guidance that does not repeat their values.
- Preserve unrelated working-tree files and existing generated artifacts; stage only files changed for the task being committed.

---

## File structure

- `internal/config/config.go` — non-secret connection config plus explicit detection of deprecated credential settings.
- `internal/config/config_test.go` — configuration defaults, overrides, and non-leaking migration errors.
- `internal/authstore/store.go` — controller-scoped API-key record, keyring integration, fallback writes, and legacy-session cleanup.
- `internal/authstore/store_test.go` — storage behavior using an injected in-memory keyring and temporary state directories.
- `internal/session/store.go` and `internal/session/store_test.go` — delete after their cleanup-compatible replacement exists; no live session storage remains.
- `internal/client/client.go` — API-key-only request construction, credential source tracking, validation, and saved-key invalidation.
- `internal/client/client_test.go` — HTTP-level tests for header use, source precedence, missing auth, and `401` behavior.
- `internal/apperr/apperr.go` and `internal/apperr/apperr_test.go` — add the stable `not_authenticated` error code.
- `internal/cli/auth.go` — retain only `auth status` behavior and shared safe auth metadata.
- `internal/cli/login.go` — top-level interactive `login` and local `logout` commands.
- `internal/cli/prompt.go` — hidden TTY API-key prompt with an injectable test seam.
- `internal/cli/root.go`, `internal/cli/helpers.go`, `internal/cli/context.go`, `internal/cli/configcmd.go` — command registration and removal of session-era flags and secret fields.
- `internal/cli/auth_test.go`, `internal/cli/cli_test.go`, `internal/cli/login_test.go` — command behavior, prompt, redaction, and help coverage.
- `go.mod`, `go.sum` — add the direct terminal-input dependency only.
- `README.md`, `configs/config.example.yaml` — public API-key-only setup and persistent-state documentation.

### Task 1: Block credential configuration and add a stable missing-auth error

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/apperr/apperr.go`
- Modify: `internal/apperr/apperr_test.go`

**Interfaces:**
- Produces: `config.Load` that never accepts or populates password/API-key configuration.
- Produces: `apperr.NotAuthenticated` with the string value `"not_authenticated"`.
- Consumed by later tasks: `config.Load(path string) (config.Config, error)` rejects legacy config keys and `UNIFI_USERNAME` / `UNIFI_PASSWORD` without exposing values.

- [ ] **Step 1: Write failing configuration and error-code tests**

  Add a table-driven `TestLoadRejectsLegacyCredentials` covering YAML `username`, `password`, and `api_key`, plus non-empty `UNIFI_USERNAME` and `UNIFI_PASSWORD`. For every case, assert `config.Load` fails, the error contains `"no longer supported"` and `"unifi login"`, and it does not contain the sentinel secret. Add a control case showing `UNIFI_API_KEY` does not change `config.Config` or make `config.Load` fail. Add an `apperr` test that `apperr.New(apperr.NotAuthenticated, "not authenticated")` has the expected code.

  ```go
  if strings.Contains(err.Error(), "legacy-secret") {
      t.Fatalf("migration error leaked secret: %v", err)
  }
  if !apperr.Is(apperr.New(apperr.NotAuthenticated, "not authenticated"), apperr.NotAuthenticated) {
      t.Fatal("missing NotAuthenticated code")
  }
  ```

- [ ] **Step 2: Run the new tests to verify they fail**

  Run: `go test ./internal/config ./internal/apperr`

  Expected: FAIL because legacy credentials are still accepted and `NotAuthenticated` does not exist.

- [ ] **Step 3: Implement safe migration checks without breaking the intermediate client build**

  Remove all credential environment overrides. Before unmarshalling into `Config`, decode the YAML top-level mapping and reject only the keys `username`, `password`, and `api_key` with a fixed error such as:

  ```go
  func legacyCredentialError(name string) error {
      return fmt.Errorf("config %q is no longer supported; remove it and run 'unifi login'", name)
  }
  ```

  Reject non-empty `UNIFI_USERNAME` and `UNIFI_PASSWORD` with equivalent fixed messages. Add `NotAuthenticated Code = "not_authenticated"` to the application error constants. Do not inspect or interpolate any credential value. Keep the three deprecated `Config` fields temporarily, but leave them unpopulated; Task 3 removes them in the same change that removes all consumers, preserving a buildable commit boundary.

- [ ] **Step 4: Run focused tests and format changed Go files**

  Run: `gofmt -w internal/config/config.go internal/config/config_test.go internal/apperr/apperr.go internal/apperr/apperr_test.go && go test ./internal/config ./internal/apperr`

  Expected: PASS.

- [ ] **Step 5: Commit the independently testable configuration boundary**

  ```bash
  git add internal/config/config.go internal/config/config_test.go internal/apperr/apperr.go internal/apperr/apperr_test.go
  git commit -m "refactor: remove credential config fields"
  ```

### Task 2: Replace session storage with controller-scoped API-key storage

**Files:**
- Create: `internal/authstore/store.go`
- Create: `internal/authstore/store_test.go`

**Interfaces:**
- Consumes: `config.Config.BaseURL()` values and the existing `go-keyring` dependency.
- Produces:

  ```go
  type Store interface {
      Load(controller string) (apiKey string, found bool, err error)
      Save(controller, apiKey string, allowFileFallback bool) error
      Delete(controller string) error
  }

  func NewStore(options Options) *KeyringStore
  func NormalizeController(controller string) (string, error)
  ```

- Consumed by later tasks: `client.NewWithStore`, `unifi login`, and `unifi logout`.

- [ ] **Step 1: Write failing storage tests around API-key records, not sessions**

  Copy the existing injected-keyring test fixture into `internal/authstore/store_test.go`. Write tests for save/load normalization, controller isolation, native-store preference, explicit fallback-only saving, fallback directory `0700` and file `0600`, atomic replacement, and deletion. Add a test with a legacy session JSON payload in the old keyring account and old `sessions` fallback path; `Load` must return `found == false` and never return the JSON as an API key. Add tests that `Save` removes the legacy fallback after a successful new-key save and `Delete` removes both current and legacy local entries.

  ```go
  key, found, err := store.Load("https://controller.example:443")
  if err != nil || found || key != "" {
      t.Fatalf("legacy record was usable: key=%q found=%t err=%v", key, found, err)
  }
  ```

- [ ] **Step 2: Run storage tests to verify they fail before implementation**

  Run: `go test ./internal/authstore`

  Expected: FAIL because `internal/authstore` and the API-key `Store` interface do not exist.

- [ ] **Step 3: Implement the API-key store and narrow legacy cleanup**

  Move the current keyring abstraction, URL normalization, account hash, state-home selection, atomic write, and permission handling into `internal/authstore`. Persist a JSON record with controller and API-key fields so `Load` can validate that the controller matches and distinguish the record from the legacy session JSON. Use the existing `unifi-cli` keyring service and controller account hash: a successful new-key save replaces a legacy keyring session record in place. Use a new fallback subdirectory such as `keys`; retain knowledge of the old `sessions` subdirectory only to delete it.

  Implement these safety rules exactly:

  ```go
  // Load reads only a valid API-key record from the new storage location.
  // A legacy or malformed record is not an API key and returns found == false.
  // Save uses the keyring first; only ErrKeyringUnavailable plus
  // allowFileFallback writes the protected fallback.
  // Delete removes the keyring account, new fallback, and legacy fallback;
  // missing entries are successful.
  ```

  Error text from storage must identify an operation but never include encoded records, fallback contents, or key values.

- [ ] **Step 4: Run storage tests and the package-wide test suite**

  Run: `gofmt -w internal/authstore/store.go internal/authstore/store_test.go && go test ./internal/authstore && go test ./...`

  Expected: PASS. The existing session package remains temporarily untouched until the client has migrated in Task 3, so the repository must still compile at this task boundary.

- [ ] **Step 5: Commit the storage replacement**

  ```bash
  git add internal/authstore/store.go internal/authstore/store_test.go
  git commit -m "feat: persist controller API keys securely"
  ```

### Task 3: Make the HTTP client API-key-only with correct source precedence

**Files:**
- Modify: `internal/client/client.go`
- Modify: `internal/client/auth.go`
- Modify: `internal/client/client_test.go`
- Modify: `internal/cli/helpers.go`
- Modify: `internal/config/config.go`
- Delete: `internal/session/store.go`
- Delete: `internal/session/store_test.go`

**Interfaces:**
- Consumes: `authstore.Store`, `apperr.NotAuthenticated`, and non-secret `config.Config`.
- Produces:

  ```go
  func New(cfg config.Config) (*Client, error)
  func NewWithStore(cfg config.Config, store authstore.Store) (*Client, error)
  func NewWithAPIKey(cfg config.Config, apiKey, method string) (*Client, error)
  func (c *Client) AuthMethod() string
  func (c *Client) Validate(ctx context.Context) error
  ```

- Contract: `AuthMethod()` returns exactly `"environment_api_key"` or `"saved_api_key"` for normal clients. The temporary validation client uses `"interactive_api_key"` and never persists or deletes state.

- [ ] **Step 1: Replace session tests with failing API-key source tests**

  Remove password-login, cookie-rotation, CSRF, and saved-session tests. Add injected in-memory `authstore.Store` tests that assert:

  - `UNIFI_API_KEY` is sent as `X-API-KEY` and wins over a saved key.
  - A saved key is loaded once and sent as `X-API-KEY` by a fresh client.
  - No environment key and no saved key produces `apperr.NotAuthenticated` with hint `run 'unifi login'` before an HTTP request.
  - A `401` from `saved_api_key` deletes only that controller's store record and returns the login hint.
  - A `401` from `environment_api_key` leaves the store untouched.
  - `NewWithAPIKey` validates an entered key without accessing the store.

  ```go
  err := c.Do(ctx, http.MethodGet, client.PathSelfSites, nil, nil)
  if !apperr.Is(err, apperr.NotAuthenticated) || requests != 0 {
      t.Fatalf("missing-key behavior: err=%v requests=%d", err, requests)
  }
  ```

- [ ] **Step 2: Run client tests to verify the new cases fail**

  Run: `go test ./internal/client`

  Expected: FAIL because the API-key-only constructors, private credential source, and invalidation behavior do not exist yet.

- [ ] **Step 3: Implement key resolution, request headers, and saved-key invalidation**

  Delete `Username`, `Password`, and `APIKey` from `config.Config`, then delete cookie-jar, CSRF token, response-cookie, password-login, read-only-session, and session-write code from `internal/client`. Store the active API key privately on `Client`; never put it back on `config.Config`. In `NewWithStore`, use non-empty `UNIFI_API_KEY` first, otherwise load from `authstore.Store`. `NewWithAPIKey` must construct the same transport with the supplied key and `interactive_api_key` method without loading the store.

  `ensureAuth` must return:

  ```go
  apperr.WithHint(
      apperr.New(apperr.NotAuthenticated, "not authenticated"),
      "run 'unifi login' to save an API key",
  )
  ```

  Set `X-API-KEY` from the private client field. After an `apperr.AuthFailed`, call `store.Delete(c.baseURL)` only when `AuthMethod() == "saved_api_key"`; preserve a deletion failure as a cause without rendering it. Update `loadRuntime` to always construct the standard API-key client and remove the `--no-session-write` branch.

- [ ] **Step 4: Run the client tests and preserve the CLI boundary for Task 4**

  Run: `gofmt -w internal/client/client.go internal/client/auth.go internal/client/client_test.go internal/cli/helpers.go internal/config/config.go && go test ./internal/client`

  Expected: PASS. Leave command registration and CLI command-test updates to Task 4.

- [ ] **Step 5: Commit the API-key-only client behavior**

  ```bash
  git add internal/client/client.go internal/client/auth.go internal/client/client_test.go internal/cli/helpers.go internal/config/config.go internal/session/store.go internal/session/store_test.go
  git commit -m "refactor: make client API-key-only"
  ```

### Task 4: Provide interactive login, local logout, and safe auth status

**Files:**
- Create: `internal/cli/login.go`
- Create: `internal/cli/login_test.go`
- Create: `internal/cli/prompt.go`
- Modify: `internal/cli/auth.go`
- Modify: `internal/cli/auth_test.go`
- Modify: `internal/cli/root.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: `client.NewWithAPIKey`, `client.NewWithStore`, `authstore.NewStore`, and `golang.org/x/term`.
- Produces:

  ```go
  func newLoginCmd() *cobra.Command
  func newLogoutCmd() *cobra.Command
  func newAuthStatusCmd() *cobra.Command
  func promptAPIKey(in *os.File, out io.Writer) (string, error)
  ```

- Contract: only `unifi login` accepts interactive key input; `unifi logout` performs no controller request; `unifi auth status` validates the resolved source with `GET client.PathSelfSites`.

- [ ] **Step 1: Write failing command and prompt tests**

  Add command tests using injectable `newAuthStore`, `newClientWithAPIKey`, and `promptAPIKey` seams. Assert that successful `unifi login` prompts once, validates with `GET /self/sites`, saves only after validation, cleans legacy entries, and emits `auth_method: saved_api_key` without the sentinel key. Assert validation failure leaves a pre-existing saved key unchanged. Assert `unifi login --file-fallback` passes `true` only to that save call. Assert non-TTY input returns `validation_failed` with `UNIFI_API_KEY` guidance. Assert `unifi logout` deletes local current and legacy entries without starting a server request. Update status tests for `saved_api_key` and `environment_api_key` only.

  ```go
  if strings.Contains(output.String(), "api-key-not-for-output") {
      t.Fatalf("login output leaked API key: %q", output.String())
  }
  if store.saveCalls != 0 {
      t.Fatalf("failed validation saved key %d times", store.saveCalls)
  }
  ```

- [ ] **Step 2: Run CLI auth tests to verify they fail**

  Run: `go test ./internal/cli`

  Expected: FAIL because only nested password-session commands and no hidden prompt exist.

- [ ] **Step 3: Implement the top-level command flow and hidden prompt**

  Add `golang.org/x/term` as a direct dependency. `promptAPIKey` must require `term.IsTerminal(int(in.Fd()))`, print `API key: ` to the command output, call `term.ReadPassword`, print one newline, trim surrounding whitespace, and reject empty input without including it in the error. Keep the function behind a package variable or small injected dependency so tests never need a real TTY.

  Register `newLoginCmd()` and `newLogoutCmd()` at the root. `unifi login` loads non-secret config, prompts, builds an `interactive_api_key` validation client, calls `Validate`, then calls `store.Save(cfg.BaseURL(), key, allowFileFallback)`. Only after both operations succeed, emit safe metadata with `saved_api_key`. `unifi logout` calls `store.Delete(cfg.BaseURL())` and emits `logged_out`; it must not construct an HTTP client. Keep `auth` only as the read-only `auth status` command and remove `auth login` and `auth logout`.

- [ ] **Step 4: Run focused command tests and inspect help output**

  Run: `gofmt -w internal/cli/login.go internal/cli/login_test.go internal/cli/prompt.go internal/cli/auth.go internal/cli/auth_test.go internal/cli/root.go && go test ./internal/cli && go run ./cmd/unifi --help`

  Expected: tests PASS and root help lists `login` and `logout`; no password-login or API-key argument flag appears. Task 5 removes the remaining obsolete `--no-session-write` flag.

- [ ] **Step 5: Commit the user-facing auth workflow**

  ```bash
  git add internal/cli/login.go internal/cli/login_test.go internal/cli/prompt.go internal/cli/auth.go internal/cli/auth_test.go internal/cli/root.go go.mod go.sum
  git commit -m "feat: add persistent API-key login"
  ```

### Task 5: Remove session-era UI surface and document the final workflow

**Files:**
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/context.go`
- Modify: `internal/cli/configcmd.go`
- Modify: `internal/cli/cli_test.go`
- Modify: `README.md`
- Modify: `configs/config.example.yaml`

**Interfaces:**
- Consumes: the command names and auth-method strings from Task 4.
- Produces: non-secret `config show` data containing `host`, `port`, `insecure`, `site`, `safe_mode`, and `timeout` only.

- [ ] **Step 1: Write failing help and config-output tests**

  Replace tests that expect nested auth login/logout, secret redaction placeholders, or `--no-session-write`. Add tests that root help includes `login` and `logout`, `auth --help` exposes only `status`, `login --help` exposes `--file-fallback` but not an API-key flag, and `config show` has no `username`, `password`, or `api_key` keys. Use a config with legacy fields and verify the failure text has no sentinel secret.

  ```go
  for _, forbidden := range []string{"username", "password", "api_key", "--no-session-write"} {
      if strings.Contains(out, forbidden) {
          t.Fatalf("obsolete auth surface %q in output:\n%s", forbidden, out)
      }
  }
  ```

- [ ] **Step 2: Run CLI surface tests to verify they fail**

  Run: `go test ./internal/cli`

  Expected: FAIL because the root still registers session-era flags and config output still includes credential fields.

- [ ] **Step 3: Remove obsolete flags and update user-facing documentation**

  Remove `flagNoSessionWrite` from `root.go` and its `loadRuntime` behavior. Remove all credential fields from `redactedConfig` and `printData` ordering. Update the README and example config to show non-secret controller configuration followed by:

  ```bash
  unifi login
  unifi auth status --json
  unifi logout
  ```

  Document the hidden prompt, restart-persistent native stores, explicit `unifi login --file-fallback`, the warning that fallback is opt-in protected local state, controller scoping, `401` re-login behavior, and `UNIFI_API_KEY` as a process-only CI/script override. Remove every username/password/session instruction and update the documented Go requirement to Go 1.26.5.

- [ ] **Step 4: Run documentation-adjacent tests and inspect forbidden live surfaces**

  Run: `gofmt -w internal/cli/root.go internal/cli/context.go internal/cli/configcmd.go internal/cli/cli_test.go && go test ./internal/cli && rg -n "UNIFI_USERNAME|UNIFI_PASSWORD|auth login|auth logout|no-session-write|username: admin" README.md configs/config.example.yaml internal/cli -g '!**/*_test.go'`

  Expected: tests PASS; the `rg` command prints no obsolete public auth instruction. Historical design and plan documents are intentionally excluded from this check.

- [ ] **Step 5: Commit the surface and documentation cleanup**

  ```bash
  git add internal/cli/root.go internal/cli/context.go internal/cli/configcmd.go internal/cli/cli_test.go README.md configs/config.example.yaml
  git commit -m "docs: document API-key-only login"
  ```

### Task 6: Verify the complete migration and review the release diff

**Files:**
- Verify: all files changed by Tasks 1–5
- Modify only if verification exposes a concrete defect in those files.

**Interfaces:**
- Consumes: the complete API-key-only CLI.
- Produces: evidence that the repository builds, tests, and does not retain an active password/session authentication path.

- [ ] **Step 1: Run the full automated verification suite**

  Run: `go test ./... && go vet ./... && go build -o /tmp/unifi-api-key-plan ./cmd/unifi`

  Expected: all commands exit successfully and the built binary is created at `/tmp/unifi-api-key-plan`.

- [ ] **Step 2: Verify command registration and safe error behavior with the built binary**

  Run:

  ```bash
  tmp_config="$(mktemp /tmp/unifi-api-key-config.XXXXXX)"
  printf 'host: controller.example\n' > "$tmp_config"
  /tmp/unifi-api-key-plan --help
  /tmp/unifi-api-key-plan login </dev/null
  /tmp/unifi-api-key-plan --config "$tmp_config" config show
  rm "$tmp_config"
  ```

  Expected: help includes top-level `login` and `logout`; non-TTY login exits with a redacted `UNIFI_API_KEY` guidance error; config show succeeds when the temporary config has a host value. Create the temporary config with a user-only mode and remove it after the check.

- [ ] **Step 3: Review the migration diff for secrets and obsolete behavior**

  Run:

  ```bash
  git diff --check fcf5b40..HEAD
  git diff fcf5b40..HEAD -- internal/config internal/authstore internal/client internal/cli README.md configs/config.example.yaml
  git status --short
  ```

  Expected: no whitespace errors; no key literal or password-login endpoint remains in active code; only task-scoped files are staged or committed. Treat test sentinel strings as test data, not credentials.

- [ ] **Step 4: Commit only a concrete verification fix, if one was required**

  If the preceding steps reveal and fix a defect, run the affected focused test plus `go test ./...`, then commit only the changed task files:

  ```bash
  git add <verified-task-files>
  git commit -m "fix: complete API-key auth migration"
  ```

  If no defect was found, do not create an empty commit.
