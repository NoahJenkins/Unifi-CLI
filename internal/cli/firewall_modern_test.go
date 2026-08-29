package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/domain"
	"github.com/noahjenkins/unifi-cli/internal/plan"
)

const (
	internalZoneID   = "ffffffff-ffff-4fff-8fff-fffffffffff1"
	externalZoneID   = "ffffffff-ffff-4fff-8fff-fffffffffff2"
	blockWebPolicyID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeee2"
	createdPolicyID  = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeee9"
)

func TestFirewallZoneCommandsExposeStableOfficialReadSurface(t *testing.T) {
	root := newFirewallCmd()
	zone, _, err := root.Find([]string{"zone"})
	if err != nil || zone == nil {
		t.Fatalf("firewall zone command missing: command=%v err=%v", zone, err)
	}
	for _, child := range []string{"list", "get", "create", "update", "delete"} {
		if command, _, err := zone.Find([]string{child}); err != nil || command == nil {
			t.Fatalf("firewall zone %s missing: command=%v err=%v", child, command, err)
		}
	}

	srv := newCommandTestServer(t)
	defer srv.Close()
	t.Run("list human", func(t *testing.T) {
		useCommandTestRuntime(t, srv, false)
		stdout, stderr, err := captureProcessOutput(t, runFirewallZoneList)
		if err != nil || stderr != "" {
			t.Fatalf("list: err=%v stdout=%q stderr=%q", err, stdout, stderr)
		}
		want := "NAME      ORIGIN          CONFIGURABLE  NETWORKS                              ID\n" +
			"External  SYSTEM_DEFINED  false         -                                     ffffffff-ffff-4fff-8fff-fffffffffff2\n" +
			"Internal  SYSTEM_DEFINED  true          cccccccc-cccc-4ccc-8ccc-ccccccccccc1  ffffffff-ffff-4fff-8fff-fffffffffff1\n"
		if stdout != want {
			t.Fatalf("zone list human = %q, want %q", stdout, want)
		}
	})

	t.Run("list JSON", func(t *testing.T) {
		useCommandTestRuntime(t, srv, true)
		stdout, stderr, err := captureProcessOutput(t, runFirewallZoneList)
		if err != nil || stderr != "" {
			t.Fatalf("list: err=%v stdout=%q stderr=%q", err, stdout, stderr)
		}
		want := `{"schema_version":"1","ok":true,"resource":"firewall","action":"zone list","data":[{"id":"ffffffff-ffff-4fff-8fff-fffffffffff2","name":"External","network_ids":[],"origin":"SYSTEM_DEFINED","configurable":false},{"id":"ffffffff-ffff-4fff-8fff-fffffffffff1","name":"Internal","network_ids":["cccccccc-cccc-4ccc-8ccc-ccccccccccc1"],"origin":"SYSTEM_DEFINED","configurable":true}],"meta":{"site":"default","dry_run":false}}`
		assertDecodedJSONEqual(t, stdout, want)
	})

	t.Run("get human", func(t *testing.T) {
		useCommandTestRuntime(t, srv, false)
		stdout, stderr, err := captureProcessOutput(t, func() error { return runFirewallZoneGet("Internal") })
		if err != nil || stderr != "" {
			t.Fatalf("get: err=%v stdout=%q stderr=%q", err, stdout, stderr)
		}
		want := "id: ffffffff-ffff-4fff-8fff-fffffffffff1\nname: Internal\norigin: SYSTEM_DEFINED\nconfigurable: true\nnetwork_ids: cccccccc-cccc-4ccc-8ccc-ccccccccccc1\n"
		if stdout != want {
			t.Fatalf("zone get human = %q, want %q", stdout, want)
		}
	})

	t.Run("get JSON", func(t *testing.T) {
		useCommandTestRuntime(t, srv, true)
		stdout, stderr, err := captureProcessOutput(t, func() error { return runFirewallZoneGet(internalZoneID) })
		if err != nil || stderr != "" {
			t.Fatalf("get: err=%v stdout=%q stderr=%q", err, stdout, stderr)
		}
		want := `{"schema_version":"1","ok":true,"resource":"firewall","action":"zone get","data":{"id":"ffffffff-ffff-4fff-8fff-fffffffffff1","name":"Internal","network_ids":["cccccccc-cccc-4ccc-8ccc-ccccccccccc1"],"origin":"SYSTEM_DEFINED","configurable":true},"meta":{"site":"default","dry_run":false}}`
		assertDecodedJSONEqual(t, stdout, want)
	})
}

func TestFirewallZoneWriteFlagsAndRiskClasses(t *testing.T) {
	create := newFirewallZoneCreateCmd()
	update := newFirewallZoneUpdateCmd()
	for _, flag := range []string{"name", "network"} {
		if create.Flags().Lookup(flag) == nil {
			t.Errorf("zone create is missing --%s", flag)
		}
	}
	for _, flag := range []string{"name", "network", "clear-networks"} {
		if update.Flags().Lookup(flag) == nil {
			t.Errorf("zone update is missing --%s", flag)
		}
	}
	want := map[string]plan.RiskClass{
		"create": plan.HighImpact,
		"update": plan.HighImpact,
		"delete": plan.Destructive,
	}
	if len(firewallZoneMutationRisk) != len(want) {
		t.Fatalf("firewall zone risk table has %d entries, want %d", len(firewallZoneMutationRisk), len(want))
	}
	for action, risk := range want {
		if got := firewallZoneMutationRisk[action]; got != risk {
			t.Errorf("firewall zone %s risk = %q, want %q", action, got, risk)
		}
	}
}

func TestFirewallZoneCommandsExactEmptyAmbiguityAndPermissionOutput(t *testing.T) {
	const zonePath = "/proxy/network/integration/v1/sites/11111111-1111-4111-8111-111111111111/firewall/zones"

	t.Run("empty", func(t *testing.T) {
		srv := newCommandTestServerWithOptions(t, commandServerOptions{officialCollections: map[string]string{zonePath: `[]`}})
		defer srv.Close()
		useCommandTestRuntime(t, srv, false)
		stdout, stderr, err := captureProcessOutput(t, runFirewallZoneList)
		if err != nil || stderr != "" || stdout != "NAME  ORIGIN  CONFIGURABLE  NETWORKS  ID\n" {
			t.Fatalf("empty human: err=%v stdout=%q stderr=%q", err, stdout, stderr)
		}
		useCommandTestRuntime(t, srv, true)
		stdout, stderr, err = captureProcessOutput(t, runFirewallZoneList)
		if err != nil || stderr != "" {
			t.Fatalf("empty JSON: err=%v stdout=%q stderr=%q", err, stdout, stderr)
		}
		assertDecodedJSONEqual(t, stdout, `{"schema_version":"1","ok":true,"resource":"firewall","action":"zone list","data":[],"meta":{"site":"default","dry_run":false}}`)
	})

	t.Run("ambiguity", func(t *testing.T) {
		duplicates := `[{"id":"ffffffff-ffff-4fff-8fff-fffffffffff1","name":"Duplicate","networkIds":[],"metadata":{"origin":"USER_DEFINED"}},{"id":"ffffffff-ffff-4fff-8fff-fffffffffff2","name":"Duplicate","networkIds":[],"metadata":{"origin":"USER_DEFINED"}}]`
		srv := newCommandTestServerWithOptions(t, commandServerOptions{officialCollections: map[string]string{zonePath: duplicates}})
		defer srv.Close()
		useCommandTestRuntime(t, srv, false)
		stdout, stderr, err := captureProcessOutput(t, func() error { return runFirewallZoneGet("Duplicate") })
		if err == nil || stdout != "" || stderr != "ambiguous_id: multiple matches for \"Duplicate\"\n" {
			t.Fatalf("ambiguity human: err=%v stdout=%q stderr=%q", err, stdout, stderr)
		}
		useCommandTestRuntime(t, srv, true)
		stdout, stderr, err = captureProcessOutput(t, func() error { return runFirewallZoneGet("Duplicate") })
		if err == nil || stderr != "" {
			t.Fatalf("ambiguity JSON: err=%v stdout=%q stderr=%q", err, stdout, stderr)
		}
		assertDecodedJSONEqual(t, stdout, `{"schema_version":"1","ok":false,"resource":"firewall","action":"zone get","data":null,"meta":{"site":"default","dry_run":false},"error":{"code":"ambiguous_id","message":"multiple matches for \"Duplicate\""}}`)
	})

	t.Run("permission", func(t *testing.T) {
		srv := newCommandTestServerWithOptions(t, commandServerOptions{officialStatuses: map[string]int{zonePath: http.StatusForbidden}})
		defer srv.Close()
		useCommandTestRuntime(t, srv, false)
		stdout, stderr, err := captureProcessOutput(t, runFirewallZoneList)
		if err == nil || stdout != "" || stderr != "permission_denied: controller returned HTTP status 403: permission denied\n" {
			t.Fatalf("permission human: err=%v stdout=%q stderr=%q", err, stdout, stderr)
		}
		useCommandTestRuntime(t, srv, true)
		stdout, stderr, err = captureProcessOutput(t, runFirewallZoneList)
		if err == nil || stderr != "" {
			t.Fatalf("permission JSON: err=%v stdout=%q stderr=%q", err, stdout, stderr)
		}
		assertDecodedJSONEqual(t, stdout, `{"schema_version":"1","ok":false,"resource":"firewall","action":"zone list","data":null,"meta":{"site":"default","dry_run":false},"error":{"code":"permission_denied","message":"controller returned HTTP status 403: permission denied"}}`)
	})
}

func TestModernFirewallCLIFlagsRemoveLegacyRulesetContract(t *testing.T) {
	create := newFirewallCreateCmd()
	update := newFirewallUpdateCmd()
	reorder := newFirewallReorderCmd()
	move := newFirewallMoveCmd()
	for _, flag := range []string{"source-zone", "destination-zone", "ip-version", "protocol", "logging-enabled", "allow-return-traffic"} {
		if create.Flags().Lookup(flag) == nil {
			t.Errorf("create is missing --%s", flag)
		}
	}
	for _, flag := range []string{"source-ip", "destination-ip", "destination-port"} {
		if create.Flags().Lookup(flag) == nil {
			t.Errorf("create is missing --%s", flag)
		}
		if update.Flags().Lookup(flag) != nil {
			t.Errorf("update unexpectedly exposes --%s", flag)
		}
	}
	for _, flag := range []string{"source-zone", "destination-zone", "before-system-ids", "after-system-ids"} {
		if reorder.Flags().Lookup(flag) == nil {
			t.Errorf("reorder is missing --%s", flag)
		}
	}
	for _, obsolete := range []string{"ruleset", "src", "dst", "index"} {
		if create.Flags().Lookup(obsolete) != nil || update.Flags().Lookup(obsolete) != nil {
			t.Errorf("policy commands still expose obsolete --%s", obsolete)
		}
	}
	for _, obsolete := range []string{"ids", "id", "index"} {
		if reorder.Flags().Lookup(obsolete) != nil {
			t.Errorf("atomic reorder still exposes legacy --%s", obsolete)
		}
	}
	for _, flag := range []string{"before", "after"} {
		if move.Flags().Lookup(flag) == nil {
			t.Errorf("move is missing --%s", flag)
		}
	}
	for _, obsolete := range []string{"source-zone", "destination-zone", "index"} {
		if move.Flags().Lookup(obsolete) != nil {
			t.Errorf("relative move unexpectedly exposes --%s", obsolete)
		}
	}
}

func TestFirewallExactCreateUsesExistingPlanApplyAndDryRunGates(t *testing.T) {
	in := domain.FirewallInput{
		Name: "Allow one TCP service", Action: "allow", AllowReturnTraffic: true, SetAllowReturnTraffic: true,
		SourceZone: "Internal", DestinationZone: "External", IPVersion: "ipv4", Protocol: "tcp",
		SourceIP: "192.0.2.10", SetSourceIP: true,
		DestinationIP: "198.51.100.20", SetDestinationIP: true,
		DestinationPort: 1514, SetDestinationPort: true,
	}

	t.Run("plan only", func(t *testing.T) {
		srv, writes := newFirewallMutationTestServer(t)
		defer srv.Close()
		useCommandTestRuntime(t, srv, true)
		stdout, _, err := captureProcessOutput(t, func() error { return runFirewallCreate(in) })
		var envelope struct {
			Plan struct {
				Changes []struct {
					After map[string]any `json:"after"`
				} `json:"changes"`
			} `json:"plan"`
		}
		decodeErr := json.Unmarshal([]byte(stdout), &envelope)
		var after map[string]any
		if len(envelope.Plan.Changes) == 1 {
			after = envelope.Plan.Changes[0].After
		}
		sourceFilter, _ := after["source_filter"].(map[string]any)
		destinationFilter, _ := after["destination_filter"].(map[string]any)
		destinationPorts, _ := destinationFilter["port_filter"].(map[string]any)
		portItems, _ := destinationPorts["items"].([]any)
		port := float64(0)
		if len(portItems) == 1 {
			portItem, _ := portItems[0].(map[string]any)
			port, _ = portItem["value"].(float64)
		}
		if err != nil || decodeErr != nil || *writes != 0 || sourceFilter["type"] != "ip_address" || destinationFilter["type"] != "ip_address" || port != float64(1514) {
			t.Fatalf("plan-only exact create: err=%v writes=%d stdout=%q", err, *writes, stdout)
		}
	})

	t.Run("dry run wins", func(t *testing.T) {
		srv, writes := newFirewallMutationTestServer(t)
		defer srv.Close()
		useCommandTestRuntime(t, srv, true)
		flagYes, flagDryRun, flagExperimental, flagForce = true, true, true, true
		stdout, _, err := captureProcessOutput(t, func() error { return runFirewallCreate(in) })
		var envelope struct {
			Meta struct {
				DryRun bool `json:"dry_run"`
			} `json:"meta"`
		}
		decodeErr := json.Unmarshal([]byte(stdout), &envelope)
		if err != nil || decodeErr != nil || *writes != 0 || !envelope.Meta.DryRun {
			t.Fatalf("dry-run exact create: err=%v writes=%d stdout=%q", err, *writes, stdout)
		}
	})

	t.Run("apply uses one write", func(t *testing.T) {
		srv, writes := newFirewallMutationTestServer(t)
		defer srv.Close()
		useCommandTestRuntime(t, srv, true)
		flagYes, flagExperimental, flagForce = true, true, true
		_, _, err := captureProcessOutput(t, func() error { return runFirewallCreate(in) })
		if err != nil || *writes != 1 {
			t.Fatalf("exact apply: err=%v writes=%d", err, *writes)
		}
	})
}

func TestFirewallExactCreateSurfacesSanitizedControllerValidation(t *testing.T) {
	srv, writes := newFirewallMutationTestServerWithPostError(
		t,
		http.StatusBadRequest,
		`{"code":"api.validation.invalid-request","message":"destination traffic filter is invalid","requestId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","requestPath":"/integration/v1/sites/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb/firewall/policies"}`,
	)
	defer srv.Close()
	useCommandTestRuntime(t, srv, true)
	flagYes, flagExperimental, flagForce = true, true, true
	in := domain.FirewallInput{
		Name: "Allow one TCP service", Action: "allow", AllowReturnTraffic: true, SetAllowReturnTraffic: true,
		SourceZone: "Internal", DestinationZone: "External", IPVersion: "ipv4", Protocol: "tcp",
		SourceIP: "192.0.2.10", SetSourceIP: true,
		DestinationIP: "198.51.100.20", SetDestinationIP: true,
		DestinationPort: 1514, SetDestinationPort: true,
	}

	stdout, stderr, err := captureProcessOutput(t, func() error { return runFirewallCreate(in) })
	if err == nil || stderr != "" || *writes != 1 {
		t.Fatalf("exact create validation: err=%v writes=%d stdout=%q stderr=%q", err, *writes, stdout, stderr)
	}
	assertDecodedJSONEqual(t, stdout, `{"schema_version":"1","ok":false,"resource":"firewall","action":"create","data":null,"meta":{"site":"default","dry_run":false,"experimental":true},"error":{"code":"validation_failed","message":"controller returned HTTP status 400: api.validation.invalid-request: destination traffic filter is invalid"}}`)
	for _, protected := range []string{
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		"/integration/v1/sites/",
	} {
		if strings.Contains(stdout, protected) {
			t.Fatalf("exact create validation rendered protected value %q: %s", protected, stdout)
		}
	}
}

func TestFirewallExactCreateAndGetExposeInspectableSchemaV1Filters(t *testing.T) {
	srv, writes := newFirewallMutationTestServer(t)
	defer srv.Close()
	in := domain.FirewallInput{
		Name: "Allow one TCP service", Action: "allow", AllowReturnTraffic: true, SetAllowReturnTraffic: true,
		SourceZone: "Internal", DestinationZone: "External", IPVersion: "ipv4", Protocol: "tcp",
		SourceIP: "192.0.2.10", SetSourceIP: true,
		DestinationIP: "198.51.100.20", SetDestinationIP: true,
		DestinationPort: 1514, SetDestinationPort: true,
	}

	useCommandTestRuntime(t, srv, true)
	flagYes, flagExperimental, flagForce = true, true, true
	_, _, err := captureProcessOutput(t, func() error { return runFirewallCreate(in) })
	if err != nil || *writes != 1 {
		t.Fatalf("seed exact policy: err=%v writes=%d", err, *writes)
	}

	useCommandTestRuntime(t, srv, true)
	stdout, stderr, err := captureProcessOutput(t, func() error { return runFirewallGet(createdPolicyID) })
	if err != nil || stderr != "" {
		t.Fatalf("JSON get: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	assertSchemaV1(t, stdout)
	var envelope struct {
		Data domain.FirewallRule `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.SourceFilter == nil || envelope.Data.DestinationFilter == nil ||
		envelope.Data.SourceFilter.IPAddressFilter == nil || envelope.Data.DestinationFilter.IPAddressFilter == nil ||
		envelope.Data.DestinationFilter.PortFilter == nil ||
		len(envelope.Data.SourceFilter.IPAddressFilter.Items) != 1 ||
		envelope.Data.SourceFilter.IPAddressFilter.Items[0].Value != "192.0.2.10" ||
		len(envelope.Data.DestinationFilter.PortFilter.Items) != 1 ||
		envelope.Data.DestinationFilter.PortFilter.Items[0].Value != 1514 {
		t.Fatalf("normalized exact policy = %#v", envelope.Data)
	}

	useCommandTestRuntime(t, srv, false)
	stdout, stderr, err = captureProcessOutput(t, func() error { return runFirewallGet(createdPolicyID) })
	if err != nil || stderr != "" ||
		!strings.Contains(stdout, `source_filter: {"type":"ip_address","ip_address_filter":{"type":"ip_addresses","match_opposite":false,"items":[{"type":"ip_address","value":"192.0.2.10"}]}}`) ||
		!strings.Contains(stdout, `destination_filter: {"type":"ip_address","ip_address_filter":{"type":"ip_addresses","match_opposite":false,"items":[{"type":"ip_address","value":"198.51.100.20"}]},"port_filter":{"type":"ports","match_opposite":false,"items":[{"type":"port_number","value":1514}]}}`) {
		t.Fatalf("human exact get: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
}

func TestFirewallWriteRiskClassesMatchApprovedTable(t *testing.T) {
	want := map[string]plan.RiskClass{
		"create":  plan.HighImpact,
		"update":  plan.HighImpact,
		"delete":  plan.Destructive,
		"reorder": plan.HighImpact,
		"move":    plan.HighImpact,
	}
	if len(firewallMutationRisk) != len(want) {
		t.Fatalf("firewall mutation risk table has %d entries, want %d", len(firewallMutationRisk), len(want))
	}
	for action, wantRisk := range want {
		if got := firewallMutationRisk[action]; got != wantRisk {
			t.Errorf("firewall %s risk = %q, want %q", action, got, wantRisk)
		}
	}
}

func TestAllFirewallWritesUseExperimentalAndForceCentralGates(t *testing.T) {
	mutations := []struct {
		name string
		run  func() error
	}{
		{name: "create", run: func() error {
			return runFirewallCreate(domain.FirewallInput{Name: "Allow HTTPS", Action: "allow", SourceZone: "Internal", DestinationZone: "External", IPVersion: "ipv4", Protocol: "tcp"})
		}},
		{name: "update", run: func() error {
			return runFirewallUpdate(commandFirewallID, domain.FirewallInput{Name: "Renamed", SetName: true})
		}},
		{name: "delete", run: func() error { return runFirewallDelete(commandFirewallID) }},
		{name: "reorder", run: func() error {
			return runFirewallReorder(domain.FirewallReorder{SourceZone: "Internal", DestinationZone: "External", BeforeSystemDefined: []string{blockWebPolicyID}, AfterSystemDefined: []string{commandFirewallID}})
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name+" experimental", func(t *testing.T) {
			srv, writes := newFirewallMutationTestServer(t)
			defer srv.Close()
			useCommandTestRuntime(t, srv, true)
			flagYes, flagForce = true, true
			stdout, _, err := captureProcessOutput(t, mutation.run)
			if err == nil || !strings.Contains(stdout, "experimental") || *writes != 0 {
				t.Fatalf("experimental gate: err=%v writes=%d stdout=%q", err, *writes, stdout)
			}
		})

		t.Run(mutation.name+" high impact", func(t *testing.T) {
			srv, writes := newFirewallMutationTestServer(t)
			defer srv.Close()
			useCommandTestRuntime(t, srv, true)
			flagYes, flagExperimental = true, true
			stdout, _, err := captureProcessOutput(t, mutation.run)
			if err == nil || !strings.Contains(stdout, "safe_mode_blocked") || *writes != 0 {
				t.Fatalf("high-impact gate: err=%v writes=%d stdout=%q", err, *writes, stdout)
			}
		})
	}
}

func TestFirewallDryRunMakesZeroWritesAndCreateReturnsObservedState(t *testing.T) {
	in := domain.FirewallInput{Name: "Allow HTTPS", Action: "allow", SourceZone: "Internal", DestinationZone: "External", IPVersion: "ipv4", Protocol: "tcp"}

	t.Run("without yes", func(t *testing.T) {
		srv, writes := newFirewallMutationTestServer(t)
		defer srv.Close()
		useCommandTestRuntime(t, srv, true)
		stdout, _, err := captureProcessOutput(t, func() error { return runFirewallCreate(in) })
		if err != nil || *writes != 0 || !strings.Contains(stdout, `"plan"`) {
			t.Fatalf("plan-only: err=%v writes=%d stdout=%q", err, *writes, stdout)
		}
	})

	t.Run("dry run", func(t *testing.T) {
		srv, writes := newFirewallMutationTestServer(t)
		defer srv.Close()
		useCommandTestRuntime(t, srv, true)
		flagYes, flagDryRun, flagExperimental, flagForce = true, true, true, true
		stdout, _, err := captureProcessOutput(t, func() error { return runFirewallCreate(in) })
		var envelope struct {
			Meta struct {
				DryRun bool `json:"dry_run"`
			} `json:"meta"`
		}
		decodeErr := json.Unmarshal([]byte(stdout), &envelope)
		if err != nil || decodeErr != nil || *writes != 0 || !envelope.Meta.DryRun {
			t.Fatalf("dry-run: err=%v writes=%d stdout=%q", err, *writes, stdout)
		}
	})

	t.Run("apply", func(t *testing.T) {
		srv, writes := newFirewallMutationTestServer(t)
		defer srv.Close()
		useCommandTestRuntime(t, srv, true)
		flagYes, flagExperimental, flagForce = true, true, true
		stdout, stderr, err := captureProcessOutput(t, func() error { return runFirewallCreate(in) })
		if err != nil || *writes != 1 || !strings.Contains(stderr, "audit: applied") {
			t.Fatalf("apply: err=%v writes=%d stdout=%q stderr=%q", err, *writes, stdout, stderr)
		}
		var envelope struct {
			Action string              `json:"action"`
			Data   domain.FirewallRule `json:"data"`
		}
		if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Action != "create" || envelope.Data.ID != createdPolicyID || envelope.Data.Name != "Allow HTTPS" || envelope.Data.SourceZoneID != internalZoneID {
			t.Fatalf("controller-observed create envelope = %+v", envelope)
		}
	})
}

func newFirewallMutationTestServer(t *testing.T) (*httptest.Server, *int) {
	return newFirewallMutationTestServerWithPostError(t, 0, "")
}

func newFirewallMutationTestServerWithPostError(t *testing.T, postStatus int, postBody string) (*httptest.Server, *int) {
	t.Helper()
	writes := new(int)
	policies := []map[string]any{
		{"id": commandFirewallID, "name": "Allow DNS", "enabled": true, "index": float64(100), "action": map[string]any{"type": "ALLOW", "allowReturnTraffic": false}, "source": map[string]any{"zoneId": internalZoneID}, "destination": map[string]any{"zoneId": externalZoneID}, "ipProtocolScope": map[string]any{"ipVersion": "IPV4", "protocolFilter": map[string]any{"type": "NAMED_PROTOCOL", "protocol": map[string]any{"name": "udp"}, "matchOpposite": false}}, "loggingEnabled": false, "metadata": map[string]any{"origin": "USER_DEFINED"}},
		{"id": blockWebPolicyID, "name": "Block Web", "enabled": true, "index": float64(110), "action": map[string]any{"type": "BLOCK"}, "source": map[string]any{"zoneId": internalZoneID}, "destination": map[string]any{"zoneId": externalZoneID}, "ipProtocolScope": map[string]any{"ipVersion": "IPV4_AND_IPV6"}, "loggingEnabled": false, "metadata": map[string]any{"origin": "USER_DEFINED"}},
	}
	ordering := map[string]any{"orderedFirewallPolicyIds": map[string]any{"beforeSystemDefined": []string{commandFirewallID}, "afterSystemDefined": []string{blockWebPolicyID}}}
	const base = "/proxy/network/integration/v1"
	const sitePath = base + "/sites/11111111-1111-4111-8111-111111111111"
	zones := []map[string]any{
		{"id": internalZoneID, "name": "Internal", "networkIds": []string{}, "metadata": map[string]any{"origin": "SYSTEM_DEFINED", "configurable": true}},
		{"id": externalZoneID, "name": "External", "networkIds": []string{}, "metadata": map[string]any{"origin": "SYSTEM_DEFINED", "configurable": false}},
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") != commandTestAPIKey {
			http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writePage := func(items any, count int) {
			body, _ := json.Marshal(items)
			_, _ = io.WriteString(w, `{"offset":0,"limit":100,"count":`+strconv.Itoa(count)+`,"totalCount":`+strconv.Itoa(count)+`,"data":`+string(body)+`}`)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == base+"/sites":
			writePage([]map[string]any{{"id": commandSiteID, "internalReference": "default", "name": "Default"}}, 1)
		case r.Method == http.MethodGet && r.URL.Path == sitePath+"/firewall/zones":
			writePage(zones, len(zones))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, sitePath+"/firewall/zones/"):
			id := strings.TrimPrefix(r.URL.Path, sitePath+"/firewall/zones/")
			for _, zone := range zones {
				if zone["id"] == id {
					_ = json.NewEncoder(w).Encode(zone)
					return
				}
			}
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == sitePath+"/firewall/policies":
			writePage(policies, len(policies))
		case r.URL.Path == sitePath+"/firewall/policies/ordering":
			if r.URL.Query().Get("sourceFirewallZoneId") != internalZoneID || r.URL.Query().Get("destinationFirewallZoneId") != externalZoneID {
				http.Error(w, `{"message":"bad zone pair"}`, http.StatusBadRequest)
				return
			}
			if r.Method == http.MethodPut {
				*writes++
				if err := json.NewDecoder(r.Body).Decode(&ordering); err != nil {
					http.Error(w, `{"message":"bad body"}`, http.StatusBadRequest)
					return
				}
			}
			_ = json.NewEncoder(w).Encode(ordering)
		case r.Method == http.MethodPost && r.URL.Path == sitePath+"/firewall/policies":
			*writes++
			if postStatus != 0 {
				w.WriteHeader(postStatus)
				_, _ = io.WriteString(w, postBody)
				return
			}
			var policy map[string]any
			if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
				http.Error(w, `{"message":"bad body"}`, http.StatusBadRequest)
				return
			}
			policy["id"], policy["index"], policy["metadata"] = createdPolicyID, float64(120), map[string]any{"origin": "USER_DEFINED"}
			policies = append(policies, policy)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(policy)
		case strings.HasPrefix(r.URL.Path, sitePath+"/firewall/policies/"):
			id := strings.TrimPrefix(r.URL.Path, sitePath+"/firewall/policies/")
			index := -1
			for i, policy := range policies {
				if policy["id"] == id {
					index = i
					break
				}
			}
			if index < 0 {
				http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
				return
			}
			switch r.Method {
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(policies[index])
			case http.MethodPut:
				*writes++
				var policy map[string]any
				_ = json.NewDecoder(r.Body).Decode(&policy)
				policy["id"], policy["index"], policy["metadata"] = id, policies[index]["index"], policies[index]["metadata"]
				policies[index] = policy
				_ = json.NewEncoder(w).Encode(policy)
			case http.MethodDelete:
				*writes++
				policies = append(policies[:index], policies[index+1:]...)
				_, _ = io.WriteString(w, `{}`)
			}
		default:
			t.Errorf("unexpected firewall request %s %s", r.Method, r.URL.String())
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		}
	}))
	return server, writes
}
