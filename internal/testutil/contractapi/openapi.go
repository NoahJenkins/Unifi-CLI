// Package contractapi provides independent, synthetic controller contracts for tests.
// It must never be imported by production commands or domain code.
package contractapi

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type noExternalLoader struct{}

func (noExternalLoader) Load(string) (any, error) {
	return nil, fmt.Errorf("external schema loading forbidden")
}

type schemaEngine struct {
	mu                     sync.Mutex
	doc                    map[string]any
	compiler               *jsonschema.Compiler
	compiled               map[string]*jsonschema.Schema
	checked                map[string]bool
	nullableDiscriminators map[string]bool
}

func newSchemaEngine(doc map[string]any) (*schemaEngine, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	c.UseLoader(noExternalLoader{})
	if err := c.AddResource("contract.json", doc); err != nil {
		return nil, fmt.Errorf("contract resource invalid")
	}
	return &schemaEngine{doc: doc, compiler: c, compiled: map[string]*jsonschema.Schema{}, checked: map[string]bool{}}, nil
}
func escape(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
func (e *schemaEngine) at(ref string) (map[string]any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("local schema reference required")
	}
	var cur any = e.doc
	for _, part := range strings.Split(ref[2:], "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch x := cur.(type) {
		case map[string]any:
			cur = x[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(x) {
				return nil, fmt.Errorf("invalid schema reference")
			}
			cur = x[i]
		default:
			return nil, fmt.Errorf("unresolved schema reference")
		}
	}
	s, ok := cur.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema object required")
	}
	return s, nil
}

// inspect fails closed on unsupported assertions, but accepts descriptive annotations.
func (e *schemaEngine) inspect(ref string, active map[string]bool) error {
	if e.checked[ref] || active[ref] {
		return nil
	}
	active[ref] = true
	defer delete(active, ref)
	s, err := e.at(ref)
	if err != nil {
		return err
	}
	for k, v := range s {
		switch k {
		case "type", "required", "enum", "const", "format", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minLength", "maxLength", "pattern", "minItems", "maxItems", "uniqueItems", "minProperties", "maxProperties":
		case "title", "description", "example", "examples", "default", "deprecated", "readOnly", "writeOnly":
		case "$ref":
			target, ok := v.(string)
			if !ok {
				return fmt.Errorf("invalid schema reference")
			}
			if err := e.inspect(target, active); err != nil {
				return err
			}
		case "properties":
			fields, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid properties")
			}
			for name := range fields {
				if err := e.inspect(ref+"/properties/"+escape(name), active); err != nil {
					return err
				}
			}
		case "items":
			if err := e.inspect(ref+"/items", active); err != nil {
				return err
			}
		case "allOf":
			parts, ok := v.([]any)
			if !ok {
				return fmt.Errorf("invalid allOf")
			}
			for i := range parts {
				if err := e.inspect(ref+"/allOf/"+strconv.Itoa(i), active); err != nil {
					return err
				}
			}
		case "additionalProperties":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("unsupported additionalProperties schema")
			}
		case "discriminator":
			d, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid discriminator")
			}
			if name, ok := d["propertyName"].(string); !ok || name == "" {
				return fmt.Errorf("invalid discriminator property")
			}
			mapping, ok := d["mapping"].(map[string]any)
			if !ok || len(mapping) == 0 {
				return fmt.Errorf("explicit discriminator mapping required")
			}
			for _, target := range mapping {
				r, ok := target.(string)
				if !ok {
					return fmt.Errorf("invalid discriminator mapping")
				}
				if err := e.inspect(r, active); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unsupported schema assertion")
		}
	}
	e.checked[ref] = true
	return nil
}
func (e *schemaEngine) schema(ref string) (*jsonschema.Schema, error) {
	if s := e.compiled[ref]; s != nil {
		return s, nil
	}
	if err := e.inspect(ref, map[string]bool{}); err != nil {
		return nil, err
	}
	s, err := e.compiler.Compile("contract.json" + ref)
	if err != nil {
		return nil, fmt.Errorf("schema compilation failed")
	}
	e.compiled[ref] = s
	return s, nil
}
func (e *schemaEngine) validate(ref string, value any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.walk(ref, value, "$", map[string]bool{})
}
func (e *schemaEngine) walk(ref string, value any, path string, active map[string]bool) error {
	// Validate ordinary assertions even when inheritance revisits an active base.
	compiled, err := e.schema(ref)
	if err != nil {
		return err
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Errorf("%s: schema assertion failed", path)
	}
	pair := ref + "\x00" + path
	if active[pair] {
		return nil
	}
	active[pair] = true
	defer delete(active, pair)
	s, _ := e.at(ref)
	if target, ok := s["$ref"].(string); ok {
		if err := e.walk(target, value, path, active); err != nil {
			return err
		}
	}
	if parts, ok := s["allOf"].([]any); ok {
		for i := range parts {
			if err := e.walk(ref+"/allOf/"+strconv.Itoa(i), value, path, active); err != nil {
				return err
			}
		}
	}
	if d, ok := s["discriminator"].(map[string]any); ok {
		obj, object := value.(map[string]any)
		if !object {
			if value == nil && e.nullableDiscriminators[ref] {
				return nil
			}
			return fmt.Errorf("%s: discriminator object required", path)
		}
		name := d["propertyName"].(string)
		key, ok := obj[name].(string)
		if !ok {
			return fmt.Errorf("%s/%s: discriminator string required", path, escape(name))
		}
		target, ok := d["mapping"].(map[string]any)[key].(string)
		if !ok {
			return fmt.Errorf("%s/%s: unknown discriminator", path, escape(name))
		}
		if err := e.walk(target, value, path, active); err != nil {
			return err
		}
	}
	if obj, ok := value.(map[string]any); ok {
		if fields, ok := s["properties"].(map[string]any); ok {
			for name := range fields {
				if v, exists := obj[name]; exists {
					if err := e.walk(ref+"/properties/"+escape(name), v, path+"/"+escape(name), active); err != nil {
						return err
					}
				}
			}
		}
	}
	if items, ok := value.([]any); ok && s["items"] != nil {
		for i, v := range items {
			if err := e.walk(ref+"/items", v, path+"/"+strconv.Itoa(i), active); err != nil {
				return err
			}
		}
	}
	return nil
}
