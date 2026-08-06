# Read-Only Live Test Suite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Add an authenticated, read-only live test runner that verifies every current read capability of unifi and emits a redacted local report.

**Architecture:** A Go package owns the safe command registry, process execution, JSON-envelope validation, dependent get checks, result aggregation, and report serialization. scripts/smoke.sh remains the public entry point: it invokes the Go runner only when UNIFI_IT=1. An injected executor makes every runner behavior unit-testable without a live controller.

**Tech Stack:** Go 1.26 standard library, Bash, existing unifi JSON envelopes.

## Global Constraints

- Never register or invoke a mutation verb: create, update, delete, rename, restart, locate, upgrade, adopt, forget, reconnect, block, unblock, enable, disable, reorder, or set.
- Never pass --yes, --dry-run, or --raw to a live command.
- Validate JSON syntax, ok:true, resource/action, and top-level data shape for every controller-backed command.
- A successful empty optional list is not_configured and skips only its matching get.
- Keep reports in dist/test-reports/, ignored by Git; reports contain no raw payloads, command arguments, stderr, credentials, or secret values.
- Continue after individual failures, write the report, then return non-zero.

---

## File Structure

~~~
internal/livetest/runner.go        # registry, validation, execution, report writing
internal/livetest/runner_test.go   # fake-executor behavior tests
cmd/unifi-live-test/main.go        # command wrapper
cmd/unifi-live-test/main_test.go   # wrapper configuration tests
scripts/smoke.sh                   # existing gate plus live-runner invocation
.gitignore                         # report exclusion
README.md                          # live-suite behavior
~~~

### Task 1: Define and verify the read-only command contract

**Files:**
- Create: internal/livetest/runner.go
- Create: internal/livetest/runner_test.go

**Interfaces:**
- Produces type Command struct { Name, Resource, Action string; Args []string; Shape DataShape; Optional bool; GetFrom *GetSpec }.
- Produces type GetSpec struct { Command Command; IDField, PortDeviceField, PortIndexField string }.
- Produces type Envelope with OK, Resource, Action, Data json.RawMessage, and Meta.Count.
- Produces type Executor interface { Run(context.Context, string, ...string) ([]byte, []byte, int, error) } and OSExecutor.
- Produces ReadOnlyCommands() []Command and Validate(Command, []byte) (Envelope, []map[string]any, error).

- [ ] **Step 1: Write the failing registry and envelope-contract tests**

Create internal/livetest/runner_test.go. Name the exact behavior each test protects:

~~~go
func TestReadOnlyCommandsExcludeEveryMutationVerb(t *testing.T) {
    forbidden := map[string]bool{
        "create": true, "update": true, "delete": true, "rename": true,
        "restart": true, "locate": true, "upgrade": true, "adopt": true,
        "forget": true, "reconnect": true, "block": true, "unblock": true,
        "enable": true, "disable": true, "reorder": true, "set": true,
    }
    for _, command := range livetest.ReadOnlyCommands() {
        for _, token := range append(append([]string{}, command.Args...), command.Name) {
            if forbidden[token] || token == "--yes" || token == "--dry-run" || token == "--raw" {
                t.Fatalf("read-only command %q contains prohibited token %q", command.Name, token)
            }
        }
    }
}

func TestValidateAcceptsMatchingListEnvelope(t *testing.T) {
    command := livetest.Command{Name: "device list", Resource: "device", Action: "list", Shape: livetest.ArrayData}
    raw := []byte("{\"ok\":true,\"resource\":\"device\",\"action\":\"list\",\"data\":[{\"id\":\"dev-1\"}],\"meta\":{\"count\":1}}")
    _, items, err := livetest.Validate(command, raw)
    if err != nil { t.Fatalf("Validate: %v", err) }
    if got, want := items[0]["id"], "dev-1"; got != want { t.Fatalf("id = %v, want %q", got, want) }
}

func TestValidateRejectsWrongActionAndNonArrayList(t *testing.T) {
    command := livetest.Command{Name: "device list", Resource: "device", Action: "list", Shape: livetest.ArrayData}
    for _, raw := range [][]byte{
        []byte("{\"ok\":true,\"resource\":\"device\",\"action\":\"get\",\"data\":[],\"meta\":{}}"),
        []byte("{\"ok\":true,\"resource\":\"device\",\"action\":\"list\",\"data\":{},\"meta\":{}}"),
    } {
        if _, _, err := livetest.Validate(command, raw); err == nil { t.Fatalf("Validate unexpectedly succeeded") }
    }
}
~~~

The registry must contain exactly these safe command paths: auth status, auth login, config path, config show, site list, device list, client list, network list, wlan list, port list, firewall list, dns list, dns resolvers list, system health, system events, and system alerts. Attach dependent gets to site, device, client, network, wlan, port, firewall, and dns list.

- [ ] **Step 2: Run the focused tests and confirm the expected failure**

Run: go test ./internal/livetest -run 'TestReadOnlyCommandsExcludeEveryMutationVerb|TestValidate' -v

Expected: FAIL because the livetest package and its exports do not exist.

- [ ] **Step 3: Implement the registry and validator**

Create runner.go with DataShape constants ArrayData and ObjectData. Set ObjectData for auth status, auth login, config path, config show, and system health; all other registered commands use ArrayData. The runner, not the registry, appends --json exactly once.

~~~go
func Validate(command Command, stdout []byte) (Envelope, []map[string]any, error) {
    var env Envelope
    if err := json.Unmarshal(stdout, &env); err != nil {
        return Envelope{}, nil, fmt.Errorf("invalid JSON: %w", err)
    }
    if !env.OK {
        return Envelope{}, nil, errors.New("envelope ok is false")
    }
    if env.Resource != command.Resource || env.Action != command.Action {
        return Envelope{}, nil, fmt.Errorf("got %s %s, want %s %s", env.Resource, env.Action, command.Resource, command.Action)
    }
    if command.Shape == ObjectData {
        var object map[string]any
        if err := json.Unmarshal(env.Data, &object); err != nil || object == nil {
            return Envelope{}, nil, errors.New("expected object data")
        }
        return env, nil, nil
    }
    var items []map[string]any
    if err := json.Unmarshal(env.Data, &items); err != nil {
        return Envelope{}, nil, fmt.Errorf("expected array data: %w", err)
    }
    if env.Meta.Count != nil && *env.Meta.Count != len(items) {
        return Envelope{}, nil, fmt.Errorf("meta count %d does not match data length %d", *env.Meta.Count, len(items))
    }
    return env, items, nil
}
~~~

Keep JSON payloads in memory only.

- [ ] **Step 4: Run the focused tests and confirm they pass**

Run: go test ./internal/livetest -run 'TestReadOnlyCommandsExcludeEveryMutationVerb|TestValidate' -v

Expected: PASS.

- [ ] **Step 5: Commit the contract layer**

~~~bash
git add internal/livetest/runner.go internal/livetest/runner_test.go
git commit -m "feat: define read-only live test contracts"
~~~

### Task 2: Execute safe checks, derive gets, and write redacted reports

**Files:**
- Modify: internal/livetest/runner.go
- Modify: internal/livetest/runner_test.go

**Interfaces:**
- Consumes the Task 1 registry, Validator, GetSpec, and Executor.
- Produces Status values pass, not_configured, fail; Result { Command, Summary string; Status Status; DurationMS int64 }; Report { StartedAt time.Time; Results []Result }; Runner.Run(context.Context) (Report, error); and WriteReport(string, Report) (string, error).

- [ ] **Step 1: Write failing execution tests with a fake executor**

Add a fake executor that keys responses by strings.Join(args, " ") and records calls. The helper must start from valid responses for every registry command, then individual tests override only the response they are characterizing.

~~~go
type fakeExecutor struct { responses map[string]string; calls []string }
func (f *fakeExecutor) Run(_ context.Context, _ string, args ...string) ([]byte, []byte, int, error) {
    key := strings.Join(args, " ")
    f.calls = append(f.calls, key)
    return []byte(f.responses[key]), nil, 0, nil
}

var fixedNow = func() time.Time { return time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC) }
func validExecutor(t *testing.T) *fakeExecutor {
    t.Helper()
    responses := map[string]string{}
    for _, command := range livetest.ReadOnlyCommands() {
        data := `{}`
        if command.Shape == livetest.ArrayData { data = `[]` }
        responses[command.Name+" --json"] = fmt.Sprintf(`{"ok":true,"resource":%q,"action":%q,"data":%s,"meta":{}}`, command.Resource, command.Action, data)
    }
    return &fakeExecutor{responses: responses}
}
func resultFor(t *testing.T, report livetest.Report, command string) livetest.Result {
    t.Helper()
    for _, result := range report.Results { if result.Command == command { return result } }
    t.Fatalf("missing result for %s", command)
    return livetest.Result{}
}

func TestRunnerRunsGetForPopulatedList(t *testing.T) {
    fake := validExecutor(t)
    fake.responses["device list --json"] = `{"ok":true,"resource":"device","action":"list","data":[{"id":"dev-1"}],"meta":{"count":1}}`
    fake.responses["device get dev-1 --json"] = `{"ok":true,"resource":"device","action":"get","data":{"id":"dev-1"},"meta":{}}`
    _, err := (livetest.Runner{Binary: "unifi", Executor: fake, Now: fixedNow}).Run(context.Background())
    if err != nil { t.Fatal(err) }
    if !slices.Contains(fake.calls, "device get dev-1 --json") { t.Fatalf("calls = %v", fake.calls) }
}

func TestRunnerMarksEmptyOptionalListNotConfigured(t *testing.T) {
    fake := validExecutor(t)
    fake.responses["firewall list --json"] = `{"ok":true,"resource":"firewall","action":"list","data":[],"meta":{"count":0}}`
    report, err := (livetest.Runner{Binary: "unifi", Executor: fake, Now: fixedNow}).Run(context.Background())
    if err != nil { t.Fatal(err) }
    if got := resultFor(t, report, "firewall list").Status; got != livetest.NotConfigured { t.Fatalf("status = %s", got) }
    if slices.Contains(fake.calls, "firewall get rule-1 --json") { t.Fatalf("unexpected get: %v", fake.calls) }
}

func TestRunnerAggregatesFailureAndStillRunsLaterChecks(t *testing.T) {
    fake := validExecutor(t)
    fake.responses["wlan list --json"] = "not json"
    report, err := (livetest.Runner{Binary: "unifi", Executor: fake, Now: fixedNow}).Run(context.Background())
    if err == nil { t.Fatal("Run unexpectedly succeeded") }
    if got := resultFor(t, report, "wlan list").Status; got != livetest.Fail { t.Fatalf("status = %s", got) }
    if !slices.Contains(fake.calls, "system health --json") { t.Fatalf("later check was skipped: %v", fake.calls) }
}

func TestWriteReportNeverPersistsPayloadOrSecret(t *testing.T) {
    path, err := livetest.WriteReport(t.TempDir(), livetest.Report{
        Results: []livetest.Result{{Command: "config show", Status: "pass", Summary: "validated"}},
    })
    if err != nil { t.Fatal(err) }
    data, err := os.ReadFile(path); if err != nil { t.Fatal(err) }
    if strings.Contains(string(data), "api_key") || strings.Contains(string(data), "s3cret") {
        t.Fatalf("report leaked protected content: %s", data)
    }
}
~~~

Use literal IDs site-1, dev-1, client-1, network-1, wlan-1, rule-1, and dns-1. The port fixture must be {"device_id":"dev-1","port_idx":1}; expect port get dev-1 1 --json.

- [ ] **Step 2: Run the focused tests and confirm the expected failure**

Run: go test ./internal/livetest -run 'TestRunner|TestWriteReport' -v

Expected: FAIL because Runner, results, reports, and WriteReport do not exist.

- [ ] **Step 3: Implement execution, dependent gets, aggregation, and report writing**

Implement Runner with Binary, Executor, Commands, and Now fields. Use ReadOnlyCommands when Commands is empty. For each command, append --json, execute it, then validate stdout only if exitCode is zero and err is nil. Record only the command name, outcome, rounded duration in milliseconds, and a sanitized error summary.

For each populated list with GetFrom, derive a get command from the first normalized item. Use the item id for normal resources. For ports, require device_id and a numeric port_idx. Missing lookup fields are failures. For an empty optional list, mark only the list outcome not_configured and skip its dependent get. Do not skip an empty non-optional list.

Continue after every failure and return errors.Join of failures after all commands have been attempted.

WriteReport must create its directory with mode 0700 and write read-only-<UTC timestamp>.json with mode 0600. Serialize only StartedAt and Results. Never serialize raw stdout, stderr, IDs, args, environment, or error payload bodies.

- [ ] **Step 4: Run the complete package tests**

Run: go test ./internal/livetest -v

Expected: PASS for derived gets, empty optional resources, failure aggregation, and report redaction.

- [ ] **Step 5: Commit the runner behavior**

~~~bash
git add internal/livetest/runner.go internal/livetest/runner_test.go
git commit -m "feat: run read-only live CLI checks"
~~~

### Task 3: Expose the runner through the existing smoke workflow

**Files:**
- Create: cmd/unifi-live-test/main.go
- Create: cmd/unifi-live-test/main_test.go
- Modify: scripts/smoke.sh
- Modify: .gitignore
- Modify: README.md

**Interfaces:**
- Consumes livetest.Runner, livetest.OSExecutor, and livetest.WriteReport.
- Produces go run ./cmd/unifi-live-test --binary <path> --report-dir <path>, returning zero only when all checks pass or are not_configured.
- Preserves UNIFI_IT=1 ./scripts/smoke.sh as the public live-test entry point.

- [ ] **Step 1: Write failing wrapper configuration tests**

Extract argument parsing into parseConfig.

~~~go
func TestParseConfigUsesDefaultReportDirectory(t *testing.T) {
    cfg, err := parseConfig([]string{"--binary", "/tmp/unifi"})
    if err != nil { t.Fatal(err) }
    if cfg.Binary != "/tmp/unifi" { t.Fatalf("binary = %q", cfg.Binary) }
    if cfg.ReportDir != "dist/test-reports" { t.Fatalf("report dir = %q", cfg.ReportDir) }
}

func TestParseConfigRejectsEmptyBinary(t *testing.T) {
    if _, err := parseConfig(nil); err == nil { t.Fatal("parseConfig unexpectedly accepted no binary") }
}
~~~

- [ ] **Step 2: Run the focused test and confirm the expected failure**

Run: go test ./cmd/unifi-live-test -v

Expected: FAIL because the command package and parseConfig do not exist.

- [ ] **Step 3: Implement wrapper, script, ignore rule, and README guidance**

Implement main.go with flag.NewFlagSet, a required --binary, and --report-dir defaulting to dist/test-reports. Construct Runner with OSExecutor and time.Now. Always write the report, including after Runner failure. Print only PASS, NOT CONFIGURED, or FAIL plus the command name and final report path. Return 1 for test failures and 2 for wrapper-flag errors.

In scripts/smoke.sh, replace the seven hard-coded live calls with:

~~~bash
echo "==> live read-only suite"
go run ./cmd/unifi-live-test --binary "$BIN" --report-dir "$ROOT/dist/test-reports"
~~~

Keep existing UNIFI_IT credential checks and the UNIFI_INSECURE default. Do not print environment values. Add dist/test-reports/ to .gitignore without changing .worktrees/ or .superpowers/. Update README Development documentation to describe complete read coverage, derived gets, not_configured outcomes, the redacted report path, and the no-mutation boundary.

- [ ] **Step 4: Run wrapper, repository, and non-live smoke validation**

Run:

~~~bash
go test ./cmd/unifi-live-test -v
go test ./...
./scripts/smoke.sh
~~~

Expected: all tests pass; smoke prints skip live IT and produces no report without UNIFI_IT=1.

- [ ] **Step 5: Verify the no-mutation boundary and run the authenticated suite**

Run:

~~~bash
rg -n 'create|update|delete|rename|restart|locate|upgrade|adopt|forget|reconnect|block|unblock|enable|disable|reorder|set|--yes|--dry-run|--raw' internal/livetest cmd/unifi-live-test scripts/smoke.sh
UNIFI_IT=1 ./scripts/smoke.sh
~~~

Expected: the first command finds no mutation registration or apply flag in runner paths (test assertions and README prose may mention names); the authenticated command writes dist/test-reports/read-only-<timestamp>.json and returns zero only for pass or not_configured outcomes.

- [ ] **Step 6: Commit the public integration**

~~~bash
git add cmd/unifi-live-test/main.go cmd/unifi-live-test/main_test.go scripts/smoke.sh .gitignore README.md
git commit -m "test: add read-only live UniFi suite"
~~~
