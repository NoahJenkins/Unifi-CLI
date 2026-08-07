package render_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/render"
)

func TestExitCode(t *testing.T) {
	if got := render.ExitCode(nil); got != 0 {
		t.Fatalf("nil => %d, want 0", got)
	}
	if got := render.ExitCode(apperr.New(apperr.ValidationFailed, "bad")); got != 2 {
		t.Fatalf("validation => %d, want 2", got)
	}
	if got := render.ExitCode(apperr.New(apperr.NotFound, "missing")); got != 1 {
		t.Fatalf("not_found => %d, want 1", got)
	}
	if got := render.ExitCode(errors.New("plain")); got != 1 {
		t.Fatalf("plain => %d, want 1", got)
	}
}

func TestWriteJSONSuccessShape(t *testing.T) {
	var buf bytes.Buffer
	env := render.Success("device", "list", "default", []any{"a", "b"}, false)
	if err := render.WriteJSON(&buf, env); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["ok"] != true {
		t.Fatalf("ok = %v", got["ok"])
	}
	if got["resource"] != "device" || got["action"] != "list" {
		t.Fatalf("resource/action = %v/%v", got["resource"], got["action"])
	}
	meta, _ := got["meta"].(map[string]any)
	if meta["site"] != "default" {
		t.Fatalf("meta.site = %v", meta["site"])
	}
	if meta["count"] != float64(2) {
		t.Fatalf("meta.count = %v", meta["count"])
	}
	if _, hasErr := got["error"]; hasErr {
		t.Fatal("success envelope must omit error")
	}
}

func TestWriteJSONFailShape(t *testing.T) {
	var buf bytes.Buffer
	err := apperr.WithHint(apperr.New(apperr.NotFound, "no device"), "use list")
	env := render.Fail("device", "get", "default", err)
	if writeErr := render.WriteJSON(&buf, env); writeErr != nil {
		t.Fatal(writeErr)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["ok"] != false {
		t.Fatalf("ok = %v", got["ok"])
	}
	eb, _ := got["error"].(map[string]any)
	if eb["code"] != "not_found" || eb["message"] != "no device" || eb["hint"] != "use list" {
		t.Fatalf("error body = %+v", eb)
	}
}

func TestWriteTableNonEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := render.WriteTable(&buf, []string{"ID", "Name"}, [][]string{{"1", "ap"}, {"2", "sw"}}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if out == "" {
		t.Fatal("expected non-empty table output")
	}
	if !strings.Contains(out, "ID") || !strings.Contains(out, "ap") {
		t.Fatalf("unexpected table: %q", out)
	}
}

func TestWriteTableEscapesTerminalControlCharacters(t *testing.T) {
	var buf bytes.Buffer
	input := "guest\x1b]52;c;YXR0YWNr\a\tname\n"
	if err := render.WriteTable(&buf, []string{"Name"}, [][]string{{input}}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, control := range []string{"\x1b", "\a", "\tname", "name\n\n"} {
		if strings.Contains(out, control) {
			t.Fatalf("table output contains terminal control sequence %q: %q", control, out)
		}
	}
	for _, visible := range []string{`\x1b`, `\a`, `\t`, `\n`} {
		if !strings.Contains(out, visible) {
			t.Fatalf("table output does not visibly escape %q: %q", visible, out)
		}
	}
}
