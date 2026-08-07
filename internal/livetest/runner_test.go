package livetest_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/noahjenkins/unifi-cli/internal/livetest"
)

type fakeResponse struct {
	stdout string
	stderr string
	exit   int
	err    error
}

type fakeExecutor struct {
	responses map[string]fakeResponse
	calls     []string
}

func (f *fakeExecutor) Run(_ context.Context, _ string, args ...string) ([]byte, []byte, int, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, key)
	response, ok := f.responses[key]
	if !ok {
		return nil, nil, 1, errors.New("missing fixture")
	}
	return []byte(response.stdout), []byte(response.stderr), response.exit, response.err
}

func validExecutor(t *testing.T) *fakeExecutor {
	t.Helper()
	responses := map[string]fakeResponse{}
	for _, command := range livetest.ReadOnlyCommands() {
		data := "{}"
		if command.Shape == livetest.ArrayData {
			data = "[]"
		}
		responses[command.Name+" --json"] = fakeResponse{
			stdout: `{"ok":true,"resource":"` + command.Resource + `","action":"` + command.Action + `","data":` + data + `,"meta":{}}`,
		}
	}
	return &fakeExecutor{responses: responses}
}

func resultFor(t *testing.T, report livetest.Report, command string) livetest.Result {
	t.Helper()
	for _, result := range report.Results {
		if result.Command == command {
			return result
		}
	}
	t.Fatalf("missing result for %s", command)
	return livetest.Result{}
}

func TestReadOnlyCommandsRejectMutationTokens(t *testing.T) {
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
	raw := []byte(`{"ok":true,"resource":"device","action":"list","data":[{"id":"dev-1"}],"meta":{"count":1}}`)

	_, items, err := livetest.Validate(command, raw)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got, want := items[0]["id"], "dev-1"; got != want {
		t.Fatalf("id = %v, want %q", got, want)
	}
}

func TestValidateRejectsInvalidListEnvelopes(t *testing.T) {
	command := livetest.Command{Name: "device list", Resource: "device", Action: "list", Shape: livetest.ArrayData}
	for _, raw := range [][]byte{
		[]byte(`{"ok":true,"resource":"device","action":"get","data":[],"meta":{}}`),
		[]byte(`{"ok":true,"resource":"device","action":"list","data":{},"meta":{}}`),
		[]byte(`{"ok":true,"resource":"device","action":"list","data":[],"meta":{"count":1}}`),
		[]byte(`{"ok":false,"resource":"device","action":"list","data":[],"meta":{}}`),
		[]byte(`not json`),
	} {
		if _, _, err := livetest.Validate(command, raw); err == nil {
			t.Fatalf("Validate(%s) unexpectedly succeeded", raw)
		}
	}
}

func TestRunnerRunsDerivedGetForPopulatedDeviceList(t *testing.T) {
	fake := validExecutor(t)
	fake.responses["device list --json"] = fakeResponse{stdout: `{"ok":true,"resource":"device","action":"list","data":[{"id":"dev-1"}],"meta":{"count":1}}`}
	fake.responses["device get dev-1 --json"] = fakeResponse{stdout: `{"ok":true,"resource":"device","action":"get","data":{"id":"dev-1"},"meta":{}}`}

	report, err := (livetest.Runner{Binary: "unifi", Executor: fake, Now: fixedNow}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fake.calls, "device get dev-1 --json") {
		t.Fatalf("calls = %v", fake.calls)
	}
	if got := resultFor(t, report, "device get").Status; got != livetest.Pass {
		t.Fatalf("device get status = %s", got)
	}
}

func TestRunnerDerivesPortGetArguments(t *testing.T) {
	fake := validExecutor(t)
	fake.responses["port list --json"] = fakeResponse{stdout: `{"ok":true,"resource":"port","action":"list","data":[{"device_id":"dev-1","port_idx":1}],"meta":{"count":1}}`}
	fake.responses["port get dev-1 1 --json"] = fakeResponse{stdout: `{"ok":true,"resource":"port","action":"get","data":{"device_id":"dev-1","port_idx":1},"meta":{}}`}

	_, err := (livetest.Runner{Binary: "unifi", Executor: fake, Now: fixedNow}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fake.calls, "port get dev-1 1 --json") {
		t.Fatalf("calls = %v", fake.calls)
	}
}

func TestRunnerMarksEmptyOptionalListNotConfigured(t *testing.T) {
	fake := validExecutor(t)
	fake.responses["firewall list --json"] = fakeResponse{stdout: `{"ok":true,"resource":"firewall","action":"list","data":[],"meta":{"count":0}}`}

	report, err := (livetest.Runner{Binary: "unifi", Executor: fake, Now: fixedNow}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := resultFor(t, report, "firewall list").Status; got != livetest.NotConfigured {
		t.Fatalf("status = %s", got)
	}
	if slices.Contains(fake.calls, "firewall get rule-1 --json") {
		t.Fatalf("unexpected get: %v", fake.calls)
	}
}

func TestRunnerRecordsFailureAndContinuesToLaterChecks(t *testing.T) {
	fake := validExecutor(t)
	fake.responses["wlan list --json"] = fakeResponse{stdout: "not json"}

	report, err := (livetest.Runner{Binary: "unifi", Executor: fake, Now: fixedNow}).Run(context.Background())
	if err == nil {
		t.Fatal("Run unexpectedly succeeded")
	}
	if got := resultFor(t, report, "wlan list").Status; got != livetest.Fail {
		t.Fatalf("status = %s", got)
	}
	if got := resultFor(t, report, "system health").Status; got != livetest.Pass {
		t.Fatalf("later check status = %s", got)
	}
}

func TestRunnerFailsWhenPopulatedListCannotDeriveGetArgument(t *testing.T) {
	fake := validExecutor(t)
	fake.responses["device list --json"] = fakeResponse{stdout: `{"ok":true,"resource":"device","action":"list","data":[{}],"meta":{"count":1}}`}

	report, err := (livetest.Runner{Binary: "unifi", Executor: fake, Now: fixedNow}).Run(context.Background())
	if err == nil {
		t.Fatal("Run unexpectedly succeeded")
	}
	if got := resultFor(t, report, "device get").Status; got != livetest.Fail {
		t.Fatalf("device get status = %s", got)
	}
}

func TestRunnerDoesNotExposeProcessStderrInFailureSummary(t *testing.T) {
	fake := validExecutor(t)
	fake.responses["client list --json"] = fakeResponse{stderr: "api_key=s3cret", exit: 7, err: errors.New("exit status 7")}

	report, err := (livetest.Runner{Binary: "unifi", Executor: fake, Now: fixedNow}).Run(context.Background())
	if err == nil {
		t.Fatal("Run unexpectedly succeeded")
	}
	result := resultFor(t, report, "client list")
	if strings.Contains(result.Summary, "s3cret") || strings.Contains(result.Summary, "api_key") {
		t.Fatalf("summary leaked stderr: %q", result.Summary)
	}
}

func TestRunnerRecordsDurationFromInjectedClock(t *testing.T) {
	fake := validExecutor(t)
	start := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	times := []time.Time{start, start.Add(10 * time.Millisecond), start.Add(37 * time.Millisecond)}
	i := 0
	now := func() time.Time {
		value := times[i]
		i++
		return value
	}

	report, err := (livetest.Runner{
		Binary:   "unifi",
		Executor: fake,
		Commands: []livetest.Command{{Name: "auth status", Resource: "auth", Action: "status", Args: []string{"auth", "status"}, Shape: livetest.ObjectData}},
		Now:      now,
	}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := report.Results[0].DurationMS, int64(27); got != want {
		t.Fatalf("duration = %dms, want %dms", got, want)
	}
}

func TestWriteReportRedactsUntrustedSummaryAndUsesPrivatePermissions(t *testing.T) {
	dir := t.TempDir()
	path, err := livetest.WriteReport(dir, livetest.Report{
		StartedAt: fixedNow(),
		Results:   []livetest.Result{{Command: "config show", Status: livetest.Fail, Summary: "api_key=s3cret"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "api_key") || strings.Contains(string(data), "s3cret") {
		t.Fatalf("report leaked protected content: %s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
			t.Fatalf("file mode = %o, want %o", got, want)
		}
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("report dir = %q, want %q", filepath.Dir(path), dir)
	}
}

var fixedNow = func() time.Time { return time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC) }
