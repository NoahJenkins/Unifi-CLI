package releasepipeline_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Execute the selector from the workflow, then resolve the scheduling values
// from its real matrices. This tests routing behavior rather than a YAML copy.
func TestCIRunnerRouting(t *testing.T) {
	jobs := mapValue(t, readYAMLMap(t, ".github/workflows/ci.yml"), "jobs")
	routing := mapValue(t, jobs, "runner-routing")
	if routing["runs-on"] != "ubuntu-latest" {
		t.Fatal("runner selection must execute on GitHub-hosted Ubuntu")
	}
	steps := jobSteps(t, routing)
	step := steps[stepIndex(t, steps, "select Linux runner")].(map[string]any)
	env := mapValue(t, step, "env")
	for key, expression := range map[string]string{
		"OPTIPLEX_CI_ENABLED": "${{ vars.OPTIPLEX_CI_ENABLED }}",
		"EVENT_NAME":          "${{ github.event_name }}",
		"HEAD_REPOSITORY":     "${{ github.event.pull_request.head.repo.full_name }}",
		"REPOSITORY":          "${{ github.repository }}",
	} {
		if env[key] != expression {
			t.Fatalf("selector input %s = %v, want %s", key, env[key], expression)
		}
	}
	outputs := mapValue(t, routing, "outputs")
	wantOutput := fmt.Sprintf("${{ steps.%s.outputs.linux_runner }}", step["id"])
	if outputs["linux_runner"] != wantOutput {
		t.Fatalf("selector output = %v, want %s", outputs["linux_runner"], wantOutput)
	}

	for _, tc := range []struct {
		name, enabled, event, head, actor, want string
	}{
		{"enabled push", "true", "push", "", "maintainer", "optiplex-ci-ubuntu24"},
		{"enabled internal PR", "true", "pull_request", "NoahJenkins/Unifi-CLI", "contributor", "optiplex-ci-ubuntu24"},
		{"internal Dependabot PR", "true", "pull_request", "NoahJenkins/Unifi-CLI", "dependabot[bot]", "optiplex-ci-ubuntu24"},
		{"disabled", "false", "push", "", "maintainer", "ubuntu-latest"},
		{"missing variable", "", "pull_request", "NoahJenkins/Unifi-CLI", "maintainer", "ubuntu-latest"},
		{"uppercase is not opt-in", "TRUE", "push", "", "maintainer", "ubuntu-latest"},
		{"mixed case is not opt-in", "True", "push", "", "maintainer", "ubuntu-latest"},
		{"whitespace is not opt-in", "true ", "push", "", "maintainer", "ubuntu-latest"},
		{"numeric is not opt-in", "1", "push", "", "maintainer", "ubuntu-latest"},
		{"fork PR", "true", "pull_request", "contributor/Unifi-CLI", "contributor", "ubuntu-latest"},
		{"Dependabot fork PR", "true", "pull_request", "contributor/Unifi-CLI", "dependabot[bot]", "ubuntu-latest"},
		{"missing PR head", "true", "pull_request", "", "maintainer", "ubuntu-latest"},
		{"unsupported event", "true", "pull_request_target", "NoahJenkins/Unifi-CLI", "maintainer", "ubuntu-latest"},
		{"untrusted value", "true\nlinux_runner=optiplex-ci-ubuntu24", "push", "", "maintainer", "ubuntu-latest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "output")
			cmd := exec.Command("bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", fmt.Sprint(step["run"]))
			cmd.Env = []string{
				"PATH=" + os.Getenv("PATH"), "GITHUB_OUTPUT=" + output,
				"OPTIPLEX_CI_ENABLED=" + tc.enabled, "EVENT_NAME=" + tc.event,
				"HEAD_REPOSITORY=" + tc.head, "REPOSITORY=NoahJenkins/Unifi-CLI", "GITHUB_ACTOR=" + tc.actor,
			}
			if result, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("selector: %v: %s", err, result)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.TrimSuffix(string(data), "\n")
			if got != "linux_runner="+tc.want {
				t.Fatalf("selector output = %q, want linux_runner=%s", got, tc.want)
			}
			context := map[string]string{"needs.runner-routing.outputs.linux_runner": tc.want}
			for _, id := range []string{"test", "native-keyring", "vulnerability-scan", "fuzz"} {
				job := mapValue(t, jobs, id)
				if !slices.Contains(stringSliceValue(t, job, "needs"), "runner-routing") {
					t.Fatalf("%s does not depend on runner selection", id)
				}
				if id == "test" || id == "native-keyring" {
					matrix := mapValue(t, mapValue(t, job, "strategy"), "matrix")
					platforms := stringSliceValue(t, matrix, "os")
					if !slices.Equal(platforms, []string{"ubuntu-latest", "macos-latest", "windows-latest"}) {
						t.Fatalf("%s logical platforms changed: %v", id, platforms)
					}
					for _, platform := range platforms {
						leg := map[string]string{"matrix.os": platform}
						for _, raw := range matrix["include"].([]any) {
							entry := raw.(map[string]any)
							if entry["os"] == platform {
								leg["matrix.runner"] = resolveCIRoutingValue(t, fmt.Sprint(entry["runner"]), context)
							}
						}
						want := platform
						if platform == "ubuntu-latest" {
							want = tc.want
						}
						if got := resolveCIRoutingValue(t, fmt.Sprint(job["runs-on"]), leg); got != want {
							t.Errorf("%s/%s runner = %s, want %s", id, platform, got, want)
						}
					}
				} else if got := resolveCIRoutingValue(t, fmt.Sprint(job["runs-on"]), context); got != tc.want {
					t.Errorf("%s runner = %s, want %s", id, got, tc.want)
				}
			}
		})
	}
}

// Scheduling uses only context lookup and || fallback. Reject other syntax so
// an unsupported expression cannot silently produce a passing policy test.
func resolveCIRoutingValue(t *testing.T, value string, context map[string]string) string {
	t.Helper()
	if !strings.HasPrefix(value, "${{") {
		return value
	}
	if !strings.HasSuffix(value, "}}") {
		t.Fatalf("invalid routing expression: %s", value)
	}
	expression := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "${{"), "}}"))
	for _, operand := range strings.Split(expression, "||") {
		key := strings.TrimSpace(operand)
		if strings.HasPrefix(key, "'") && strings.HasSuffix(key, "'") {
			return strings.Trim(key, "'")
		}
		result, ok := context[key]
		if !ok && key != "matrix.runner" {
			t.Fatalf("unsupported routing operand: %s", key)
		}
		if result != "" {
			return result
		}
	}
	return ""
}

func TestCIRoutingSelectorFailureRetainsRequiredChecks(t *testing.T) {
	jobs := mapValue(t, readYAMLMap(t, ".github/workflows/ci.yml"), "jobs")
	context := map[string]string{"needs.runner-routing.outputs.linux_runner": ""}
	for _, id := range []string{"test", "native-keyring", "vulnerability-scan", "fuzz"} {
		t.Run(id, func(t *testing.T) {
			job := mapValue(t, jobs, id)
			// A status function overrides the implicit success() dependency
			// gate. !cancelled() still honors workflow cancellation.
			if job["if"] != "${{ !cancelled() }}" {
				t.Fatal("required checks must run after selector failure while honoring cancellation")
			}
			if id == "test" || id == "native-keyring" {
				matrix := mapValue(t, mapValue(t, job, "strategy"), "matrix")
				for _, platform := range stringSliceValue(t, matrix, "os") {
					leg := map[string]string{"matrix.os": platform}
					for _, raw := range matrix["include"].([]any) {
						entry := raw.(map[string]any)
						if entry["os"] == platform {
							leg["matrix.runner"] = resolveCIRoutingValue(t, fmt.Sprint(entry["runner"]), context)
						}
					}
					if got := resolveCIRoutingValue(t, fmt.Sprint(job["runs-on"]), leg); got != platform {
						t.Errorf("fallback runner = %s, want %s", got, platform)
					}
				}
			} else if got := resolveCIRoutingValue(t, fmt.Sprint(job["runs-on"]), context); got != "ubuntu-latest" {
				t.Errorf("fallback runner = %s, want ubuntu-latest", got)
			}
		})
	}
}

func TestCICheckContextsRemainStable(t *testing.T) {
	jobs := mapValue(t, readYAMLMap(t, ".github/workflows/ci.yml"), "jobs")
	for id, name := range map[string]string{
		"test": "test (${{ matrix.os }})", "native-keyring": "native keyring (${{ matrix.os }})",
		"vulnerability-scan": "govulncheck", "fuzz": "fuzz (${{ matrix.target }})",
	} {
		if got := mapValue(t, jobs, id)["name"]; got != name {
			t.Errorf("%s check context = %v, want %s", id, got, name)
		}
	}
	fuzz := mapValue(t, jobs, "fuzz")
	matrix := mapValue(t, mapValue(t, fuzz, "strategy"), "matrix")
	var targets []string
	for _, raw := range matrix["include"].([]any) {
		entry := raw.(map[string]any)
		targets = append(targets, fmt.Sprintf("%s:%s", entry["package"], entry["target"]))
	}
	assertStringSet(t, "fuzz workload and check contexts", targets, []string{
		"./internal/config:FuzzStrictYAMLConfiguration",
		"./internal/client:FuzzOfficialPaginationJSON",
		"./internal/client:FuzzOfficialPathAndFilterEscaping",
		"./cmd/release-smoke:FuzzReleaseBundleExtraction",
		"./cmd/release-smoke:FuzzChecksumManifest",
		"./cmd/release-smoke:FuzzSBOMSnapshotParsing",
	})
}
