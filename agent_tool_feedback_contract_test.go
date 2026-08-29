package releasepipeline_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type agentToolGapIssueForm struct {
	Name   string                   `yaml:"name"`
	Title  string                   `yaml:"title"`
	Labels []string                 `yaml:"labels"`
	Body   []agentToolGapIssueField `yaml:"body"`
}

type agentToolGapIssueField struct {
	Type       string `yaml:"type"`
	ID         string `yaml:"id"`
	Attributes struct {
		Options []string `yaml:"options"`
		Value   string   `yaml:"value"`
	} `yaml:"attributes"`
	Validations struct {
		Required bool `yaml:"required"`
	} `yaml:"validations"`
}

func TestAgentToolGapIssueFormContract(t *testing.T) {
	wantName := "Agent tool gap"
	wantTitle := "[Agent Tool Gap] "
	wantLabels := []string{"agent:tool-gap", "tool:unifi-cli"}
	wantIDs := []string{
		"tool_version", "report_kind", "summary", "expected_capability",
		"observed_behavior", "reproduction", "operating_system", "source_repository",
	}

	raw, err := os.ReadFile(".github/ISSUE_TEMPLATE/agent-tool-gap.yml")
	if err != nil {
		t.Fatalf("read agent tool-gap issue form: %v", err)
	}

	var form agentToolGapIssueForm
	if err := yaml.Unmarshal(raw, &form); err != nil {
		t.Fatalf("parse agent tool-gap issue form: %v", err)
	}

	if form.Name != wantName {
		t.Errorf("name = %q, want %q", form.Name, wantName)
	}
	if form.Title != wantTitle {
		t.Errorf("title = %q, want %q", form.Title, wantTitle)
	}
	if !reflect.DeepEqual(form.Labels, wantLabels) {
		t.Errorf("labels = %q, want %q", form.Labels, wantLabels)
	}
	if len(form.Body) == 0 || form.Body[0].Type != "markdown" {
		t.Fatal("first issue-form block must be markdown safety guidance")
	}

	firstMarkdown := strings.ToLower(form.Body[0].Attributes.Value)
	for _, phrase := range []string{
		"public", "credentials", "controller responses", "hostnames", "addresses",
		"identifiers", "network inventory", "local paths", "screenshots", "raw logs",
	} {
		if !strings.Contains(firstMarkdown, phrase) {
			t.Errorf("first markdown safety guidance must mention %q", phrase)
		}
	}
	if !strings.Contains(firstMarkdown, "security defects") || !strings.Contains(firstMarkdown, "private advisories") {
		t.Error("first markdown safety guidance must direct security defects to private advisories")
	}
	if !strings.Contains(firstMarkdown, "security.md") {
		t.Error("first markdown safety guidance must reference SECURITY.md")
	}

	gotIDs := make([]string, 0, len(form.Body))
	fieldsByID := make(map[string]agentToolGapIssueField)
	for _, field := range form.Body {
		if field.Type == "markdown" {
			continue
		}
		gotIDs = append(gotIDs, field.ID)
		fieldsByID[field.ID] = field
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Errorf("field IDs = %q, want %q", gotIDs, wantIDs)
	}

	wantTypes := map[string]string{
		"tool_version":        "input",
		"report_kind":         "dropdown",
		"summary":             "textarea",
		"expected_capability": "textarea",
		"observed_behavior":   "textarea",
		"reproduction":        "textarea",
		"operating_system":    "dropdown",
		"source_repository":   "input",
	}
	for _, id := range wantIDs {
		field, ok := fieldsByID[id]
		if !ok {
			t.Errorf("required field %q is missing", id)
			continue
		}
		if field.Type != wantTypes[id] {
			t.Errorf("field %q type = %q, want %q", id, field.Type, wantTypes[id])
		}
		if !field.Validations.Required {
			t.Errorf("field %q must be required", id)
		}
	}

	if got, want := fieldsByID["report_kind"].Attributes.Options, []string{"bug", "missing-capability", "documentation"}; !reflect.DeepEqual(got, want) {
		t.Errorf("report_kind options = %q, want %q", got, want)
	}
}
