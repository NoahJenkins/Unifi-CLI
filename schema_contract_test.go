package releasepipeline_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestSchemaV1IsCheckedIn(t *testing.T) {
	if _, err := os.Stat("schemas/schema-v1.json"); err != nil {
		t.Fatalf("schema-v1 contract is missing: %v", err)
	}
}

func loadSchemaV1(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile("schemas/schema-v1.json")
	if err != nil {
		t.Fatalf("read schema-v1: %v", err)
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode schema-v1: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema-v1.json", document); err != nil {
		t.Fatalf("add schema-v1 resource: %v", err)
	}
	schema, err := compiler.Compile("schema-v1.json")
	if err != nil {
		t.Fatalf("compile schema-v1: %v", err)
	}
	return schema
}

func decodeJSONDocument(t *testing.T, raw string) any {
	t.Helper()
	var document any
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatalf("decode test document: %v", err)
	}
	return document
}

func TestSchemaV1AcceptsContractEnvelopes(t *testing.T) {
	schema := loadSchemaV1(t)
	tests := map[string]string{
		"stable success": `{"schema_version":"1","ok":true,"resource":"version","action":"show","data":{"version":"v1.0.0","commit":"abc123","build_date":"2026-08-16T00:00:00Z","go_version":"go1.26.6"},"meta":{"site":"","dry_run":false}}`,
		"config show":    `{"schema_version":"1","ok":true,"resource":"config","action":"show","data":{"profile":"home","path":"/tmp/home.yaml","host":"192.0.2.1","port":443,"insecure":false,"ca_cert":"","site":"default","safe_mode":true,"timeout":"30s"},"meta":{"site":"default","dry_run":false}}`,
		"profile list":   `{"schema_version":"1","ok":true,"resource":"config","action":"profile list","data":[{"name":"home","path":"/tmp/home.yaml","selected":true,"valid":true}],"meta":{"site":"","dry_run":false}}`,
		"profile show":   `{"schema_version":"1","ok":true,"resource":"config","action":"profile show","data":{"profile":"home","path":"/tmp/home.yaml","host":"192.0.2.1","port":443,"insecure":false,"ca_cert":"","site":"default","safe_mode":true,"timeout":"30s","selected":true,"valid":true},"meta":{"site":"default","dry_run":false}}`,
		"profile select": `{"schema_version":"1","ok":true,"resource":"config","action":"profile select","data":{"profile":"home","path":"/tmp/home.yaml"},"meta":{"site":"","dry_run":false}}`,
		"doctor":         `{"schema_version":"1","ok":true,"resource":"doctor","action":"doctor","data":{"version":"v1.1.0","commit":"abc123","config_path":"/tmp/home.yaml","profile":"home","host":"192.0.2.1","site":"default","tls_mode":"system_roots","credential_source":"saved_api_key","ready":true},"meta":{"site":"default","dry_run":false}}`,
		"doctor failure": `{"schema_version":"1","ok":false,"resource":"doctor","action":"doctor","data":{"version":"v1.1.0","commit":"abc123","config_path":"/tmp/home.yaml","profile":"home","host":"192.0.2.1","site":"default","tls_mode":"system_roots","credential_source":"missing","ready":false},"meta":{"site":"default","dry_run":false},"error":{"code":"not_authenticated","message":"no API key is available","hint":"run unifi login"}}`,
		"failure":        `{"schema_version":"1","ok":false,"resource":"device","action":"get","data":null,"meta":{"site":"default","dry_run":false},"error":{"code":"not_found","message":"device not found"}}`,
		"plan":           `{"schema_version":"1","ok":true,"resource":"network","action":"update","data":null,"meta":{"site":"default","dry_run":true},"plan":{"summary":"Update network LAN","changes":[{"op":"update","resource":"network","id":"network-1","before":{"name":"LAN"},"after":{"name":"Trusted"}}]}}`,
		"experimental":   `{"schema_version":"1","ok":true,"resource":"device","action":"restart","data":{"accepted":true},"meta":{"site":"default","dry_run":false,"experimental":true}}`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if err := schema.Validate(decodeJSONDocument(t, raw)); err != nil {
				t.Fatalf("valid envelope rejected: %v", err)
			}
		})
	}
}

func TestSchemaV1RejectsContractViolations(t *testing.T) {
	schema := loadSchemaV1(t)
	tests := map[string]string{
		"missing required field":  `{"schema_version":"1","ok":true,"resource":"version","action":"show","data":{},"meta":{"site":"","dry_run":false}}`,
		"unknown top-level field": `{"schema_version":"1","ok":true,"resource":"version","action":"show","data":{"version":"v1.0.0","commit":"abc","build_date":"date","go_version":"go1.26.6"},"meta":{"site":"","dry_run":false},"extra":true}`,
		"failure with data":       `{"schema_version":"1","ok":false,"resource":"device","action":"get","data":{},"meta":{"site":"default","dry_run":false},"error":{"code":"not_found","message":"missing"}}`,
		"doctor failure ready":    `{"schema_version":"1","ok":false,"resource":"doctor","action":"doctor","data":{"version":"v1.1.0","commit":"abc123","config_path":"/tmp/home.yaml","profile":"home","host":"192.0.2.1","site":"default","tls_mode":"system_roots","credential_source":"missing","ready":true},"meta":{"site":"default","dry_run":false},"error":{"code":"not_authenticated","message":"no API key is available"}}`,
		"failure without error":   `{"schema_version":"1","ok":false,"resource":"device","action":"get","data":null,"meta":{"site":"default","dry_run":false}}`,
		"success with error":      `{"schema_version":"1","ok":true,"resource":"version","action":"show","data":{"version":"v1.0.0","commit":"abc","build_date":"date","go_version":"go1.26.6"},"meta":{"site":"","dry_run":false},"error":{"code":"internal","message":"bad"}}`,
		"plan not dry run":        `{"schema_version":"1","ok":true,"resource":"network","action":"update","data":null,"meta":{"site":"default","dry_run":false},"plan":{"summary":"Update","changes":[{"op":"update","resource":"network"}]}}`,
		"wrong stable data":       `{"schema_version":"1","ok":true,"resource":"version","action":"show","data":{"id":"device-1"},"meta":{"site":"","dry_run":false}}`,
		"unknown stable pair":     `{"schema_version":"1","ok":true,"resource":"device","action":"restart","data":{"accepted":true},"meta":{"site":"default","dry_run":false}}`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if err := schema.Validate(decodeJSONDocument(t, raw)); err == nil {
				t.Fatal("invalid envelope was accepted")
			}
		})
	}
}
