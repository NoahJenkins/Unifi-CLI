package releasepipeline_test

import (
	"fmt"
	"slices"
	"testing"
)

// Every CI job schedules directly on a GitHub-hosted runner. No selector job
// or runner output can move a required check onto another pool.
func TestCIJobsUseHostedRunners(t *testing.T) {
	jobs := mapValue(t, readYAMLMap(t, ".github/workflows/ci.yml"), "jobs")
	for id, want := range map[string]string{
		"vulnerability-scan": "ubuntu-latest", "fuzz": "ubuntu-latest",
		"test": "${{ matrix.os }}", "native-keyring": "${{ matrix.os }}",
	} {
		job := mapValue(t, jobs, id)
		if got := job["runs-on"]; got != want {
			t.Errorf("%s runs-on = %v, want %s", id, got, want)
		}
		if _, ok := job["needs"]; ok {
			t.Errorf("%s must not depend on another job for scheduling", id)
		}
		if want == "${{ matrix.os }}" {
			matrix := mapValue(t, mapValue(t, job, "strategy"), "matrix")
			if platforms := stringSliceValue(t, matrix, "os"); !slices.Equal(platforms, []string{"ubuntu-latest", "macos-latest", "windows-latest"}) {
				t.Errorf("%s platforms = %v", id, platforms)
			}
			if _, ok := matrix["include"]; ok {
				t.Errorf("%s matrix must not add runner overrides", id)
			}
		}
	}
	if len(jobs) != 4 {
		t.Errorf("CI jobs = %d, want 4; add hosted-runner coverage for new jobs", len(jobs))
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
