package releasepipeline_test

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestDependabotAutoMergeWorkflow(t *testing.T) {
	const path = ".github/workflows/dependabot-auto-merge.yml"
	workflow := readYAMLMap(t, path)
	on := mapValue(t, workflow, "on")
	pullRequest := mapValue(t, on, "pull_request")
	if !slices.Equal(stringSliceValue(t, pullRequest, "branches"), []string{"main"}) {
		t.Errorf("pull_request branches = %v", pullRequest["branches"])
	}
	if !slices.Equal(stringSliceValue(t, pullRequest, "types"), []string{"opened", "reopened", "synchronize", "ready_for_review"}) {
		t.Errorf("pull_request types = %v", pullRequest["types"])
	}
	assertWritePermissions(t, workflow)
	concurrency := mapValue(t, workflow, "concurrency")
	if !strings.Contains(fmt.Sprint(concurrency["group"]), "github.event.pull_request.number") {
		t.Errorf("concurrency group = %v", concurrency["group"])
	}
	if cancel, ok := concurrency["cancel-in-progress"].(bool); !ok || !cancel {
		t.Errorf("cancel-in-progress = %#v, want true", concurrency["cancel-in-progress"])
	}

	jobs := mapValue(t, workflow, "jobs")
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	job := mapValue(t, jobs, "auto-merge")
	if fmt.Sprint(job["runs-on"]) != "ubuntu-latest" {
		t.Errorf("runs-on = %v", job["runs-on"])
	}
	if fmt.Sprint(job["timeout-minutes"]) != "5" {
		t.Errorf("timeout-minutes = %v, want 5", job["timeout-minutes"])
	}
	condition := fmt.Sprint(job["if"])
	if !strings.Contains(condition, "github.event.pull_request.user.login == 'dependabot[bot]'") || !strings.Contains(condition, "draft == false") {
		t.Errorf("job if = %q", condition)
	}

	seen := assertFullSHAActions(t, workflow)
	if !seen["dependabot/fetch-metadata"] {
		t.Error("auto-merge workflow missing SHA-pinned dependabot/fetch-metadata")
	}
	text := readWorkflow(t, path)
	for _, want := range []string{
		"gh pr merge --auto --squash",
		"needs-review",
		"version-update:semver-patch",
		"version-update:semver-minor",
		"version-update:semver-major",
		"github_actions",
		"GHSA-",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("auto-merge workflow missing %q", want)
		}
	}
	if strings.Contains(text, "--approve") || strings.Contains(text, "pr review") {
		t.Error("auto-merge workflow must not approve pull requests")
	}
	if strings.Contains(text, "COPILOT") || strings.Contains(text, "DEPENDABOT_CONTROLLER") {
		t.Error("retired controller is still wired into the auto-merge workflow")
	}
}

func TestGoToolchainPatchWorkflow(t *testing.T) {
	const path = ".github/workflows/go-toolchain-patch.yml"
	workflow := readYAMLMap(t, path)
	on := mapValue(t, workflow, "on")
	if _, ok := on["workflow_dispatch"]; !ok {
		t.Error("workflow must allow manual dispatch")
	}
	schedule, ok := on["schedule"].([]any)
	if !ok || len(schedule) != 1 {
		t.Fatalf("schedule = %#v", on["schedule"])
	}
	entry, ok := schedule[0].(map[string]any)
	if !ok || fmt.Sprint(entry["cron"]) != "30 16 * * 1" {
		t.Errorf("cron = %#v", schedule[0])
	}
	assertWritePermissions(t, workflow)
	concurrency := mapValue(t, workflow, "concurrency")
	if cancel, ok := concurrency["cancel-in-progress"].(bool); !ok || cancel {
		t.Errorf("cancel-in-progress = %#v, want false", concurrency["cancel-in-progress"])
	}

	job := mapValue(t, mapValue(t, workflow, "jobs"), "bump")
	if fmt.Sprint(job["if"]) != "github.ref == 'refs/heads/main'" {
		t.Errorf("job if = %v", job["if"])
	}
	if fmt.Sprint(job["runs-on"]) != "ubuntu-latest" {
		t.Errorf("runs-on = %v", job["runs-on"])
	}
	if fmt.Sprint(job["timeout-minutes"]) != "10" {
		t.Errorf("timeout-minutes = %v, want 10", job["timeout-minutes"])
	}
	seen := assertFullSHAActions(t, workflow)
	for _, action := range []string{"actions/checkout", "actions/setup-go"} {
		if !seen[action] {
			t.Errorf("toolchain workflow missing SHA-pinned %s", action)
		}
	}
	text := readWorkflow(t, path)
	for _, want := range []string{
		"go-version-file: go.mod",
		"https://go.dev/dl/?mode=json",
		"go run ./scripts/gotoolchain",
		"gh pr merge --auto --squash",
		"automated/go-",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("toolchain workflow missing %q", want)
		}
	}
	if strings.Contains(text, "go-version:") || strings.Contains(text, "--approve") || strings.Contains(text, "pr review") {
		t.Error("toolchain workflow must follow go.mod and must not approve pull requests")
	}
}

func assertWritePermissions(t *testing.T, workflow map[string]any) {
	t.Helper()
	perms := mapValue(t, workflow, "permissions")
	if len(perms) != 2 || fmt.Sprint(perms["contents"]) != "write" || fmt.Sprint(perms["pull-requests"]) != "write" {
		t.Errorf("permissions = %v, want contents and pull-requests write", perms)
	}
}

func assertFullSHAActions(t *testing.T, workflow map[string]any) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	for _, use := range allUses(workflow) {
		name, _, ok := actionRef(use)
		if !ok {
			t.Errorf("action is not pinned to a full immutable SHA: %q", use)
			continue
		}
		seen[name] = true
	}
	if len(seen) == 0 {
		t.Error("workflow has no actions")
	}
	return seen
}

func readWorkflow(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
