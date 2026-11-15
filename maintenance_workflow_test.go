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
	pullRequest := mapValue(t, on, "pull_request_target")
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
	} {
		if !strings.Contains(text, want) {
			t.Errorf("auto-merge workflow missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"DEPENDABOT_METADATA_TOKEN",
		"actions/checkout",
		"PR_BODY",
		"DEPS_JSON",
		"updated-dependencies-json",
		"This update includes a security fix",
		"This is a security update",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("auto-merge workflow still treats release-note text as an advisory via %q", forbidden)
		}
	}
	if strings.Contains(text, "--approve") || strings.Contains(text, "pr review") {
		t.Error("auto-merge workflow must not approve pull requests")
	}
	assertGHRepoForGHCommands(t, job)
	if strings.Contains(text, "COPILOT") || strings.Contains(text, "DEPENDABOT_CONTROLLER") {
		t.Error("retired controller is still wired into the auto-merge workflow")
	}
}

func assertGHRepoForGHCommands(t *testing.T, job map[string]any) {
	t.Helper()
	saw := false
	for _, raw := range jobSteps(t, job) {
		step, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("step = %#v, want mapping", raw)
		}
		script, _ := step["run"].(string)
		if !strings.Contains(script, "gh ") {
			continue
		}
		saw = true
		env, _ := step["env"].(map[string]any)
		if fmt.Sprint(env["GH_REPO"]) != "${{ github.repository }}" {
			t.Errorf("step %q runs gh without GH_REPO ${{ github.repository }}: %#v", step["name"], env)
		}
	}
	if !saw {
		t.Error("auto-merge job has no gh command to bind to GH_REPO")
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
