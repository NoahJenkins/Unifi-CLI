package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfigUsesDefaultReportDirectory(t *testing.T) {
	cfg, err := parseConfig([]string{"--binary", "/tmp/unifi"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Binary, "/tmp/unifi"; got != want {
		t.Fatalf("binary = %q, want %q", got, want)
	}
	if got, want := cfg.ReportDir, "dist/test-reports"; got != want {
		t.Fatalf("report directory = %q, want %q", got, want)
	}
}

func TestParseConfigRejectsEmptyBinary(t *testing.T) {
	if _, err := parseConfig(nil); err == nil {
		t.Fatal("parseConfig unexpectedly accepted no binary")
	}
}

func TestRunWritesReportAndReturnsFailureWhenChecksFail(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	reportDir := t.TempDir()

	code := run([]string{"--binary", "/does/not/exist", "--report-dir", reportDir}, &stdout, &stderr)
	if got, want := code, 1; got != want {
		t.Fatalf("exit code = %d, want %d; stderr = %s", got, want, stderr.String())
	}
	if !strings.Contains(stdout.String(), "FAIL auth status") || !strings.Contains(stdout.String(), "report: ") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	entries, err := os.ReadDir(reportDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".json" {
		t.Fatalf("report entries = %v", entries)
	}
}
