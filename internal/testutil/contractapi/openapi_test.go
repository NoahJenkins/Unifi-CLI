package contractapi

import (
	"encoding/json"
	"strings"
	"testing"
)

// Independently authored toy contract: inheritance dispatch must terminate,
// and missing leaf fields must still fail. No vendor schema is embedded.
func toyContract() map[string]any {
	var doc map[string]any
	_ = json.Unmarshal([]byte(`{"components":{"schemas":{
 "Base":{"type":"object","required":["kind"],"properties":{"kind":{"type":"string"}},"discriminator":{"propertyName":"kind","mapping":{"LEAF":"#/components/schemas/Leaf"}}},
 "Leaf":{"allOf":[{"$ref":"#/components/schemas/Base"},{"type":"object","required":["size"],"properties":{"size":{"type":"integer","minimum":1},"child":{"$ref":"#/components/schemas/Base"}}}]}
 }}}`), &doc)
	return doc
}
func TestDiscriminatorInheritance(t *testing.T) {
	e, err := newSchemaEngine(toyContract())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"leaf", `{"kind":"LEAF","size":2}`, true},
		{"nested", `{"kind":"LEAF","size":2,"child":{"kind":"LEAF","size":1}}`, true},
		{"lowercase", `{"kind":"leaf","size":2}`, false},
		{"missing kind", `{"size":2}`, false},
		{"nonstring kind", `{"kind":3,"size":2}`, false},
		{"missing subtype field", `{"kind":"LEAF"}`, false},
		{"subtype bound", `{"kind":"LEAF","size":0}`, false},
		{"nested invalid", `{"kind":"LEAF","size":2,"child":{"kind":"leaf","size":1}}`, false},
		{"separate instances", `{"kind":"LEAF","size":2,"child":{"kind":"LEAF"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var value any
			_ = json.Unmarshal([]byte(tc.body), &value)
			err := e.validate("#/components/schemas/Base", value)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t, want %t; error=%v", err == nil, tc.valid, err)
			}
			if err != nil && strings.Contains(err.Error(), "leaf") {
				t.Fatal("error exposed value")
			}
		})
	}
}
func TestSchemaEngineRejectsUnsupportedAndExternalRules(t *testing.T) {
	for _, body := range []string{
		`{"$ref":"https://invalid.test/schema"}`,
		`{"oneOf":[{"type":"string"},{"type":"integer"}]}`,
		`{"$ref":"#/components/schemas/Missing"}`,
	} {
		var rule any
		_ = json.Unmarshal([]byte(body), &rule)
		doc := map[string]any{"components": map[string]any{"schemas": map[string]any{"Bad": rule}}}
		e, err := newSchemaEngine(doc)
		if err == nil {
			err = e.validate("#/components/schemas/Bad", map[string]any{})
		}
		if err == nil {
			t.Fatal("unsupported contract accepted")
		}
	}
}

func TestDiscriminatorContainers(t *testing.T) {
	doc := toyContract()
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	delete(schemas["Base"].(map[string]any), "type")
	leaf := schemas["Leaf"].(map[string]any)["allOf"].([]any)[1].(map[string]any)
	delete(leaf, "type")
	e, err := newSchemaEngine(doc)
	if err != nil {
		t.Fatal(err)
	}
	ref := "#/components/schemas/Base"
	for _, value := range []any{true, "", []any{}, nil} {
		if e.validate(ref, value) == nil {
			t.Fatalf("accepted nonobject discriminator container %T", value)
		}
	}
	e.nullableDiscriminators = map[string]bool{ref: true}
	if err := e.validate(ref, nil); err != nil {
		t.Fatal("explicit null allowance rejected:", err)
	}
	if e.validate(ref, true) == nil {
		t.Fatal("null allowance accepted scalar")
	}
	if e.validate(ref, map[string]any{"kind": "LEAF"}) == nil {
		t.Fatal("null allowance skipped object subtype rules")
	}
}
