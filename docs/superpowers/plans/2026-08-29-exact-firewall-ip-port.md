# Exact Firewall IPv4 and Destination Port Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a fail-closed official firewall create mode for one source IPv4, one destination IPv4, and one TCP destination port.

**Architecture:** Extend the existing `FirewallInput` and create body without adding a second mutation path. Add loss-aware typed read normalization for the applicable official IP-address and port unions, then verify the exact-ID detail response with strict supported-scope parsing plus complete canonical writable-document equality.

**Tech Stack:** Go from `go.mod`, Cobra, JSON Schema 2020-12, synthetic official OpenAPI fixtures, repository shell gates.

**Spec:** `docs/superpowers/specs/2026-08-29-exact-firewall-ip-port-design.md`

## Global Constraints

- Use only the official local UniFi Network 10.4.57+ integration API.
- Keep firewall create experimental and high-impact.
- Apply requires `--experimental --force --yes` with default safe mode; `--dry-run` always wins.
- Make at most one write and never retry an ambiguous mutation.
- Use RFC 5737 addresses and synthetic UUIDs only.
- Do not perform a live controller mutation.
- Preserve existing zone-only behavior and complete writable documents.
- Do not add filter-update flags or unrelated firewall features.

---

### Task 1: Exact create input and official request

**Files:**
- Modify: `internal/domain/firewall.go`
- Modify: `internal/domain/firewall_test.go`
- Modify: `internal/cli/firewall.go`
- Modify: `internal/cli/firewall_modern_test.go`
- Modify: `internal/cli/update_flags_test.go`

**Interfaces:**
- Consumes: existing `FirewallInput`, `firewallCreateBody`, and `newFirewallCreateCmd`.
- Produces: `SourceIP string`, `DestinationIP string`, `DestinationPort int`, and matching `Set...` presence fields on `FirewallInput`; `firewallExactFilterMode(FirewallInput) bool`; strict create validation; the three create-only flags.

- [ ] **Step 1: Write the failing domain request and validation tests**

Add a literal expected POST body containing source and destination
`IP_ADDRESS`/`IP_ADDRESSES` filters, one `IP_ADDRESS` item per endpoint, one
destination `PORTS` filter with one `PORT_NUMBER` item, and every applicable
`matchOpposite` set to false. Add table cases for partial flag sets, IPv6,
malformed addresses, CIDR, range, hostname, `any`, comma-separated addresses,
whitespace, ports 0 and 65536, non-allow action, missing return traffic,
non-IPv4 scope, and non-TCP protocol.

- [ ] **Step 2: Run the focused domain tests and verify RED**

Run:

```bash
go test ./internal/domain -run 'TestFirewallExact|TestFirewallCreate'
```

Expected: compile or assertion failures because exact-filter input and request
construction do not exist.

- [ ] **Step 3: Write the failing CLI flag and gate tests**

Require all three flags on `firewall create`, require none on `firewall
update`, execute the intended synthetic command through the TLS test server,
and assert plan-only, experimental gate, force gate, dry-run precedence, one
POST on apply, and zero writes for invalid input.

- [ ] **Step 4: Run the focused CLI tests and verify RED**

Run:

```bash
go test ./internal/cli -run 'TestFirewallExact|TestModernFirewallCLIFlags|TestFirewallDryRun'
```

Expected: failures because the create command has no exact-filter flags.

- [ ] **Step 5: Implement the minimum request path**

Use `net/netip.ParseAddr` without trimming or coercion, require `Is4()`, and
require the full exact-filter bundle with allow/return-traffic/IPv4/TCP. Build
the exact nested official maps directly in `firewallCreateBody`. Keep the
existing zone-only branch byte-for-byte compatible.

- [ ] **Step 6: Run Task 1 tests and verify GREEN**

Run both focused commands from Steps 2 and 4. Expected: PASS with no warnings.

- [ ] **Step 7: Commit Task 1**

Stage only the five Task 1 files and commit:

```bash
git commit -m "Add exact firewall IP and port create input"
```

### Task 2: Exact post-create verification and no-retry proof

**Files:**
- Modify: `internal/domain/firewall.go`
- Modify: `internal/domain/firewall_test.go`

**Interfaces:**
- Consumes: the exact request body from Task 1 and existing exact-ID detail read.
- Produces: strict exact-filter scope parser and canonical complete-document equality in `ApplyCreateBound`.

- [ ] **Step 1: Write failing exact verification tests**

Add one success case and literal mutation cases for missing or changed enabled,
action, return traffic, zones, IPv4, TCP, source IP, destination IP, and port.
Add broadened cases for opposite matching, extra IP/port items, subnet, range,
matching-list reference, source port, MAC selector, connection state, IPsec,
and schedule. Assert one POST and one exact-ID GET for each post-write
verification result.

- [ ] **Step 2: Write the failing ambiguous-write test**

Configure the fake POST to return an error after recording the attempt. Assert
one POST, no retry, and no verification GET.

- [ ] **Step 3: Run verification tests and verify RED**

Run:

```bash
go test ./internal/domain -run 'TestFirewallExactCreateVerification|TestFirewallExactCreateWriteFailure'
```

Expected: broadened or malformed observed documents are not all rejected by a
typed exact-scope check, or the named verification helper is absent.

- [ ] **Step 4: Implement strict verification**

Require a UUID response ID. Parse the observed exact-filter scope into typed
values while rejecting absent, extra, unknown, or malformed fields. Compare
each required security field and use `wireDocumentsEqualAtPaths` for complete
canonical writable-document equality so JSON integer decoding cannot create a
false mismatch.

- [ ] **Step 5: Run Task 2 and existing firewall mutation tests**

Run:

```bash
go test ./internal/domain -run 'TestFirewall'
```

Expected: PASS, including the existing zone-only create, update-preservation,
delete, reorder, and move tests.

- [ ] **Step 6: Commit Task 2**

Stage only the two Task 2 files and commit:

```bash
git commit -m "Verify exact firewall policy security scope"
```

### Task 3: Inspectable normalized filters and schema-v1

**Files:**
- Modify: `internal/domain/firewall.go`
- Modify: `internal/domain/firewall_test.go`
- Modify: `internal/cli/firewall.go`
- Modify: `internal/cli/firewall_modern_test.go`
- Modify: `internal/cli/resource_commands_test.go`
- Modify: `schemas/schema-v1.json`
- Modify: `schema_contract_test.go`

**Interfaces:**
- Consumes: official source/destination `trafficFilter` maps.
- Produces: optional `source_filter` and `destination_filter` on `FirewallRule`; closed schema definitions for traffic, IP, and port filters/items.

- [ ] **Step 1: Write failing normalization tests**

Use a synthetic exact-filter policy and assert the complete normalized source
and destination filter objects, including item counts, item types, values,
port value, and false opposite fields. Also assert that a zone-only policy
omits both optional JSON properties.

- [ ] **Step 2: Write failing schema and CLI golden tests**

Validate exact-filter list/get output against schema-v1 and assert that an
extra filter property, missing discriminator, invalid port, or unknown item
type is rejected. Update the human `firewall get` expectation to print the
normalized exact scope without changing the zone-only list table.

- [ ] **Step 3: Run normalization and schema tests and verify RED**

Run:

```bash
go test ./internal/domain ./internal/cli . -run 'TestFirewall.*Filter|TestSchemaV1.*Firewall'
```

Expected: failures because normalized filter fields and schema definitions do
not exist.

- [ ] **Step 4: Implement typed loss-aware normalization**

Add closed Go types for outer filter type, IP filter type, `match_opposite`,
typed IP items (`ip_address`, `subnet`, `ip_address_range`), optional matching
list ID, typed port items (`port_number`, `port_number_range`), and optional
matching list ID. Preserve every applicable item. For an outer filter outside
this bounded union, report its normalized `type` without adding write support.

- [ ] **Step 5: Update schema-v1 and render output**

Add optional closed `source_filter` and `destination_filter` properties to
`firewallPolicy`. Add human detail lines only when filters are present. Keep
zone-only JSON and list-table output compatible.

- [ ] **Step 6: Run Task 3 and all firewall tests**

Run:

```bash
go test ./internal/domain ./internal/cli . -run 'TestFirewall|TestSchemaV1'
```

Expected: PASS.

- [ ] **Step 7: Commit Task 3**

Stage only the seven Task 3 files and commit:

```bash
git commit -m "Expose firewall traffic filters in schema v1"
```

### Task 4: Documentation, complete verification, and PR

**Files:**
- Modify: `README.md`
- Modify: `docs/compatibility.md`
- Modify: `CHANGELOG.md`
- Modify: `docs/superpowers/specs/2026-08-29-exact-firewall-ip-port-design.md`
- Modify: `docs/superpowers/plans/2026-08-29-exact-firewall-ip-port.md`

**Interfaces:**
- Consumes: the final command, safety, JSON, and compatibility behavior.
- Produces: current public documentation and a ready-for-review GitHub PR.

- [ ] **Step 1: Update current documentation**

Document the exact supported command, all-or-none exact-filter bundle,
rejections, optional normalized filter shape, existing delete rollback, and
the unchanged experimental/high-impact gates. State that no live firewall
mutation ran and sacrificial-controller qualification is absent.

- [ ] **Step 2: Run focused and repository gates**

Run in order:

```bash
go test ./internal/domain ./internal/cli .
go run ./cmd/release-smoke --native
./scripts/smoke.sh
go test -race ./... -timeout 30m
./scripts/check-coverage.sh
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
```

Expected: every command exits 0. Do not run `UNIFI_IT=1` or any live mutation.

- [ ] **Step 3: Review the integrated diff once**

Compare `origin/main...HEAD` plus the final worktree diff against every user
requirement. Check official-only paths, one-write behavior, exact verification,
schema closure, synthetic data, current docs, and no secrets or live data.
Resolve all critical or important review findings and rerun affected gates.

- [ ] **Step 4: Commit documentation and final fixes**

Stage only confirmed task files and commit:

```bash
git commit -m "Document exact firewall policy filters"
```

- [ ] **Step 5: Push and open one ready-for-review PR**

Push `codex/firewall-exact-ip-port` and open one non-draft PR against `main`.
Include the exact command surface, safety and verification guarantees, test
results, compatibility limits, and the statement: `No live controller mutation was performed.`

- [ ] **Step 6: Verify hosted handoff**

Confirm the PR base/head, exact head SHA, ready-for-review state, mergeability,
and reported checks. Do not merge or publish a release.
