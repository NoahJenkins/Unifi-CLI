# API Contract Emulator Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans to implement this plan task by task. Track steps below. Publication was approved as one draft PR after local verification.

**Goal:** Test real CLI mutations against independent offline contracts and pinned official schemas on a stateful local TLS server.

**Architecture:** A test utility owns rules, profiles, synthetic seeds, validation, and state. CLI tests use the production client and command layer. Cached OpenAPI validation is explicit and never downloads during tests.

**Tech Stack:** Go 1.27.3, httptest, existing jsonschema/v6; no new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-30-api-contract-emulator-design.md`

## Global constraints

- Only 10.4.57 and 10.6.109 profiles; unknown profiles fail closed.
- No committed OpenAPI, controller material, credentials, or live writes.
- Unit tests use loopback only and need no external network.
- Non-DNS mutations remain experimental; preserve CLI public contracts.
- Commit, push, and one draft PR are authorized. Merge and release are outside scope.

## Review focus

- Inherited discriminator cycles must terminate without skipping subtype rules (task 1).
- Optional-field loss must fail an independent preservation assertion (task 3).
- Accepted writes with failed responses must not retry (tasks 2 and 3).
- Ambient profiles, TLS settings, and safe-mode overrides must not affect tests (task 3).
- An unavailable or mismatched opted-in cache must fail rather than skip (task 1).

### Task 1: Independent contract validators

**Files:** `internal/testutil/contractapi/{profile,rules,openapi,seeds,contract_test}.go`, `scripts/fetch-test-openapi.sh`, `.gitignore`, package README.

**Interfaces:** `ProfileFor(version string) (Profile, error)`; `NewValidator(profile Profile, cacheDir string) (*Validator, error)`; `Validate(resource string, response bool, value any) error`; independently authored synthetic seed constructors. Empty cacheDir selects offline mode. A nonempty cacheDir requires a verified file. Validation errors expose paths/rule names only.

- [x] Write and run failing tests for unknown keys, inherited branches, missing subtype fields, invalid nested bounds, unknown profiles, cache absence/digest mismatch, unsupported schema features and external references.
- [x] Implement profiles, ordinary JSON Schema assertions plus cycle-safe discriminator traversal, and reduced Go rules for the approved seed matrix. Add a public-source-only download script and ignored cache path.
- [x] Run the same valid/invalid corpus through offline and cached validators for both profiles; distinguish unsupported coverage from schema rejection. Validate seeds/responses separately from writable requests.
- [x] Run `go test ./internal/testutil/contractapi -count=1` and its explicit cached variant. Expected: all tests pass, including original lowercase `tcp` rejection.

### Task 2: Stateful TLS API emulator

**Files:** `internal/testutil/contractapi/{server,server_test}.go`.

**Interfaces:** `NewServer(t testing.TB, profile Profile, validator *Validator) *Server`; server exposes TLS endpoint, seeded state snapshots, mutation counts, and deterministic drift/read-back/ambiguous-response hooks. It consumes task 1 validators and seeds, not production domain helpers.

- [x] Write failing tests for official routes, pagination, POST/PUT/DELETE state transitions, invalid-write atomicity, seed isolation, and accepted-write response faults.
- [x] Implement locked state, immutable copies, replacement PUT, response validation, deterministic IDs, and bounded diagnostics. Add only read dependencies needed by approved commands.
- [x] Run `go test -race ./internal/testutil/contractapi -count=1`. Expected: all tests pass; malformed requests leave state unchanged, ambiguous writes persist exactly once.

### Task 3: Real CLI scenarios and maintenance contract

**Files:** `internal/cli/contract_runtime_test.go`, `internal/cli/firewall_contract_test.go`, `internal/cli/network_contract_test.go`, `internal/cli/wlan_contract_test.go`, `CONTRIBUTING.md`, `docs/compatibility.md`.

**Interfaces:** Serial CLI harness configures a temporary profile/configuration, server CA and dummy credential, restores all flags/environment, and runs real command parsing/client requests. Seed-derived expectations are independent of submitted bodies.

- [x] Write failing firewall lifecycle and network/WLAN matrix tests, including gates, drift, verification mismatch, ambiguous response, and optional-field preservation.
- [x] Implement test harness and scenarios. Assert exact mutation counts, immutable identities, full expected writable state, and schema-v1 outcomes. Keep production semantics unchanged unless a demonstrated bug requires a separately explained fix.
- [x] Verify ambient settings cannot change gates or access local credentials/CA files. Verify redirects cannot leave the local server and proxy routing is disabled by the actual client transport.
- [x] Run the complete matrix in offline and cached modes. Add recurring maintainer-check instructions, coverage manifests, evidence limits, and safe cache handling.
- [x] Run `./scripts/smoke.sh`, `go test -race ./...`, `./scripts/check-coverage.sh`, and `go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...`. Expected: all gates pass. Use a disposable full clone only if build-info tests require it.
- [x] Obtain an independent whole-change review, fix material findings, rerun affected checks, and report local completion with remaining limits.

## Execution record

- Design approved; existing isolated branch `t3/design-api-contract-emulator` retained. Native execution selected to keep shared validator/server interfaces in one context. No publication authorized.
- Baseline `go test ./...` passed before implementation.
- Tasks 1–3 are implemented locally. The approved digest-bound correction resolves the contradictory named-protocol enums in memory; original cache bytes remain unchanged.
- Current CLI scope preserves tag filters but does not select tags. Missing gateway mDNS response coverage uses an explicit fault because both schemas require the field. WLAN update is routine and does not require force; tests retain the existing policy.
- Independent review found scalar discriminator containers were accepted in cached mode and POST could overwrite a seeded policy. Focused regressions failed before fixes, then passed under both profiles. Default adapter tests also cover typeless discriminator containers and explicit null allowance.
- Verification: smoke, full race suite, coverage, and pinned govulncheck passed. Final smoke, race, coverage, and both-profile cached checks also passed after review fixes. Internal coverage is 78.1%. No live checks ran.
- Delivery update: the user approved one draft PR containing all three implementation tasks. The initial three-PR split was for review size, not a technical dependency. Merge and release are outside scope.
