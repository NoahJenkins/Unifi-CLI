# UniFi CLI v1.0 Readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close all audit findings, complete the approved official-API feature expansion, and publish verified `v1.0.0-rc.2` and `v1.0.0` releases.

**Architecture:** Keep the stable surface on the official local integration API and preserve the existing CLI/domain/client package boundaries. Stabilize all official DNS policy types, add official read-only switching resources, and keep non-DNS writes experimental until a dedicated disposable controller can verify them. Bind publication to protected `main`, an account-restricted immutable tag, and a protected release environment.

**Tech Stack:** Go 1.26.6, Cobra/pflag, yaml.v3, JSON Schema 2020-12 with `github.com/santhosh-tekuri/jsonschema/v6`, GitHub Actions, GoReleaser, Syft, and GitHub CLI.

## Global Constraints

- Use only official local integration API routes for stable commands.
- Keep legacy device, client, port, and resolver writes experimental.
- Keep all non-DNS writes experimental because no sacrificial controller is available.
- Raise the supported UniFi Network floor from 10.3.58 to 10.4.57.
- Never put API keys or WLAN passphrases in arguments, plans, JSON, logs, fixtures, reports, issues, or release text.
- Use test-first red-green-refactor cycles for every behavior change.
- Do not contact a controller, mutate GitHub settings, create credentials, commit, push, tag, or publish without the required confirmation at that boundary.
- Preserve the existing local `main` checkout and work only in `.worktrees/codex-v1-readiness`.

---

### Task 1: Close local security and fail-closed findings

**Files:** `internal/config/config.go`, `internal/config/config_test.go`, `internal/client/client.go`, `internal/client/client_test.go`

**Interfaces:** Configuration loading rejects unknown YAML keys. A saved-key 401 clears only the current client snapshot and never deletes persisted state; `logout` remains the explicit persistent delete operation.

- [x] Add `TestLoadRejectsUnknownFields` cases for misspelled `site`, `safe_mode`, `insecure`, `host`, and `ca_cert`; run `go test ./internal/config -run TestLoadRejectsUnknownFields -count=1` and confirm the current permissive decoder fails the test.
- [x] Decode the config through `yaml.Decoder.KnownFields(true)` after the existing legacy-credential scan, reject trailing YAML documents, and preserve current error prefixes; rerun the config package tests.
- [x] Replace the same-process delete expectation with tests that require zero persistent deletes after one or many saved-key 401 responses, and add a two-client rotation test in which a delayed stale 401 cannot remove the new key; confirm both fail first.
- [x] Remove automatic `Store.Delete` from `doWithAuth`, keep the in-memory compare-and-clear guard, and retain the `login` hint; run `go test ./internal/client ./internal/authstore -count=1`.

### Task 2: Correct and harden release automation

**Files:** `.github/workflows/release.yml`, `scripts/release-metadata.sh`, `scripts/release-preflight.sh`, `scripts/publish-release.sh`, root release tests

**Interfaces:** `scripts/release-metadata.sh TAG` emits tab-separated `version`, `prerelease`, and `docs/releases/<version>.md`. Stable tags publish with `prerelease=false`; tags containing a SemVer prerelease component publish with `prerelease=true`.

- [x] Add failing script tests for `v1.0.0`, `v1.0.0-rc.2`, valid build metadata, malformed tags, mismatched notes, a commit outside `origin/main`, and stable/prerelease GitHub API fields.
- [x] Add the shared release-metadata parser, replace both hard-coded RC.1 note paths, and pass the derived prerelease Boolean to release creation and final publication.
- [x] After checkout, fetch `origin/main` and require `git merge-base --is-ancestor "$RELEASE_COMMIT" origin/main`; keep the existing exact tag binding, moved-tag, draft replacement, isolated rebuild, byte read-back, and attestation checks.
- [x] Set `environment: release` only on the write-capable publish job and add tests that reject write permission outside that one protected job.
- [x] Run `go test . -run 'Release|Publish|Preflight' -count=1` and `go run ./cmd/release-smoke --all`.

### Task 3: Stabilize complete official DNS policy CRUD

**Files:** `internal/domain/dns.go`, `internal/domain/dns_test.go`, `internal/domain/dns_stable_test.go`, `internal/cli/dns.go`, DNS CLI tests

**Interfaces:** `dns create` adds `--type a|aaaa|cname|mx|txt|srv|forward-domain` with default `a`. Common `--name` and `--enabled` remain. Type fields are `--ipv4`/legacy `--ip`, `--ipv6`, `--target-domain`, `--mail-server-domain`, `--text`, `--server-domain`, `--server-ip`, `--priority`, `--service`, `--protocol`, `--port`, `--weight`, and `--ttl`. Update infers and preserves the existing type; type changes are rejected.

- [x] Add table tests for required, forbidden, zero-valued, boundary, JSON, plan, create, update, delete, and exact verification behavior for A, AAAA, CNAME, MX, TXT, SRV, and forwarded-domain policies; confirm current non-A writes fail.
- [x] Replace the A-only input with a tagged typed input that builds one canonical expected full document. Use that document for the plan, request, and exact post-write comparison, including ID and type. Reject an invalid preserved writable field instead of substituting a default.
- [x] Preserve `--ip` as an A-record compatibility alias, reject conflicting aliases and irrelevant type flags, and keep create/update routine plus delete destructive safety classes.
- [x] Verify delete by captured ID and exact domain absence for every type, without claiming success on a different same-name policy.
- [x] Run focused domain and CLI DNS tests, then `go test ./internal/domain ./internal/cli -count=1`.

### Task 4: Complete the approved official feature expansion

**Files:** resource implementations under `internal/domain/`, Cobra commands under `internal/cli/`, synthetic official fixtures, and focused tests

**Interfaces:** Add stable reads `switching lag list|get`, `switching mc-lag list|get`, `switching stack list|get`, and `radius profile list|get`. Add stable reads plus experimental writes at `firewall traffic-list list|get|create|update|delete`; add experimental `firewall zone create|update|delete` and `firewall move <policy> (--before <policy>|--after <policy>)`.

- [x] Add failing fixture-backed list/get tests for LAG, MC-LAG, switch-stack, RADIUS profile, traffic-matching-list, and firewall-zone detail normalization. Prove stable reads make only official calls and retain the existing pagination, size, timeout, and deterministic-order limits.
- [x] Implement the read services and commands with exact ID/name resolution, typed schema-v1 output, and no raw upstream documents.
- [x] Add failing plan/apply tests for custom-zone and traffic-list CRUD. Support traffic-list types `ports`, `ipv4-addresses`, and `ipv6-addresses`; preserve complete official writable documents on PUT and verify exact observed state.
- [x] Add failing move tests for before/after exclusivity, missing targets, no-op moves, zone-pair mismatch, concurrent order drift, system-policy injection, and exact final order. Implement move by reading the complete official ordering, calculating the complete replacement, performing one atomic PUT, and verifying the entire final order.
- [x] Extend network typed flags with switch `--device`, `--enabled`, `--dhcp-mode none|server|relay`, range start/end, lease duration, conflict detection, repeatable relay/DNS server values, and domain name. Require all target-mode required fields for a management transition and never infer a DHCP range.
- [x] Extend WLAN security with `wpa3-personal`, `wpa2-wpa3-personal`, `wpa2-enterprise`, `wpa2-wpa3-enterprise`, and `wpa3-enterprise`; expose typed PMF, SAE, roaming, RADIUS-profile/NAS-ID, COA, and WPA3 security-mode flags. Continue to accept personal passphrases only through the hidden prompt or bounded stdin.
- [x] Keep every non-DNS apply behind `--experimental`; keep classic firewall and official read-only switching writes explicitly unsupported. Run focused resource tests and the complete domain/CLI suites.

### Task 5: Make schema-v1 and coverage executable contracts

**Files:** `schemas/schema-v1.json`, schema validation tests, `scripts/check-coverage.sh`, direct `internal/buildinfo` and `internal/fileutil` tests

**Interfaces:** The checked-in JSON Schema defines the required top-level envelope and resource/action data variants for every stable command. Experimental output can validate the common envelope but is not added to the frozen stable data union.

- [x] Add `jsonschema/v6@v6.0.3` as a test dependency and first add a test that fails because `schemas/schema-v1.json` is absent.
- [x] Define success, failure, plan, metadata, error, and stable resource/action variants. Require the six top-level fields, `data:null` on failure, conditional `error` and `plan`, and reject unknown top-level fields.
- [x] Route all existing and new stable JSON golden outputs through schema validation, including empty lists, ambiguity, permission, validation, drift, dry-run, accepted-action, and version responses.
- [x] Change coverage to the complete `./internal/...` tree with a 75.0% total floor and package floors: CLI 65%, client 85%, config 80%, domain 75%, authstore 65%, and privatefile 75%. Add direct buildinfo and fileutil tests before enforcing the floors.
- [x] Run `go test ./... -count=1` and `./scripts/check-coverage.sh`.

### Task 6: Add fuzz, performance, native-keyring, and documentation checks

**Files:** focused `*_test.go` fuzz/benchmark files, `scripts/check-performance.sh`, CI workflow, documentation contract test

**Interfaces:** Performance is a release-host gate, not a wall-clock CI gate. Native keyring smoke uses only unique synthetic controller names and synthetic keys, then deletes its own entry.

- [x] Add seed-based fuzz targets for strict YAML configuration, official pagination JSON, official path/filter escaping, release bundle extraction, checksum manifests, and SBOM snapshot parsing. Standard tests execute seeds; release qualification runs each target for 30 seconds.
- [x] Add benchmarks for a 10,000-item collection and 1,000 detail reads with a maximum of four concurrent requests. Set release-host budgets to under 2 seconds and 256 MiB allocated for the collection, and under 10 seconds and 256 MiB allocated for details.
- [x] Add a warm `unifi --help` benchmark with a 500 ms median release-host budget. Make `scripts/check-performance.sh` run three samples and fail only on the designated darwin/arm64 release host.
- [x] Add an opt-in native keyring round-trip integration test and CI jobs for macOS Keychain, Windows Credential Manager, and Linux Secret Service under an isolated DBus session. Test save, load, overwrite, delete, and missing-key behavior without a controller.
- [x] Add a standard-library documentation contract test for local Markdown links, release-note references, help command names, and documented command examples.

### Task 7: Refresh dependencies and stable documentation

**Files:** `go.mod`, `go.sum`, release workflow pins and SBOM snapshot, README, compatibility, security policy, changelog, release notes, maintainer checklist

- [x] Update the Go directive to 1.26.6, Syft to 1.51.0 with its immutable action SHA and snapshot, pflag to 1.0.10, and the schema test dependency; run `go mod tidy` and `go mod verify`.
- [x] Add RC.2 and stable release notes. Replace RC.1 and Task 9 language, raise the compatibility floor to 10.4.57, and document the exact stable/experimental/unsupported feature matrix.
- [x] Document release-host performance budgets, native-keyring evidence, schema-v1 location, signed-tag verification, installation, support policy, and the lack of live proof for non-DNS writes.
- [x] Align race commands with the 30-minute repository timeout without reducing test coverage.

### Task 8: Verify the complete local candidate

- [x] Run formatting and diff checks, focused security reproductions, every package test, and the schema, coverage, fuzz-seed, documentation, release-contract, release-smoke, race, module, and vulnerability gates.
- [x] Run the release-host performance gate and record exact benchmark results in the RC.2 notes.
- [x] Inspect the final diff for secret material, controller identifiers, raw live payloads, unrelated changes, and accidental stabilization of legacy or non-DNS writes.
- [x] Re-run the three original security triggers and prove the tag authorization control is represented by repository tests plus active provider rules.

### Task 9: Apply provider controls, qualify, and release

**Provider confirmation gate:** Obtain explicit confirmation immediately before changing GitHub rules/environments or publishing.

- [x] Use the authenticated NoahJenkins GitHub account for release authorization. Do not create or register a release signing key; this solo-maintainer model does not claim cryptographic tag signing or independent approval.
- [x] Keep `Protect main` at zero approvals under the selected solo model and state that the gate is not independent. GitHub ruleset 20911786 now blocks creation, update, deletion, and non-fast-forward changes for `v*` tags except for the NoahJenkins account. Require one approval if another trusted collaborator is added later.
- [x] GitHub environment `release` now requires NoahJenkins as the manual reviewer and allows self-review. Keep the workflow ancestry and exact-SHA checks authoritative because this is not independent review.
- [ ] Publish the repository changes through a focused PR, require all configured CI and CodeQL checks, resolve every review thread, and merge the exact reviewed candidate.
- [ ] Run read-only compatibility on UniFi Network 10.4.57 or later. Obtain separate explicit approval before temporary live DNS writes; run disabled, uniquely named lifecycles for all seven DNS policy types, capture IDs, delete only captured IDs, and prove exact baseline restoration. Do not run any non-DNS live write.
- [ ] Create the account-controlled `v1.0.0-rc.2` tag on the unchanged protected-main commit, confirm environment approval, verify all six artifacts, checksums, SBOMs, attestations, installs, native commands, and downloaded bytes.
- [ ] With the selected gate-only policy, create account-controlled `v1.0.0` on the same unchanged source commit after RC.2 gates pass. Verify `prerelease=false`, stable notes, installs, release metadata, support links, and final remote rules/tag/release state.

## Acceptance Criteria

- All local and hosted gates are green on the exact stable source commit.
- The three security findings and strict-config defect have passing regressions.
- Stable DNS CRUD is exact and live-verified for every official policy type.
- Non-DNS writes remain experimental and classic firewall remains unsupported.
- Stable JSON output validates against the checked-in schema.
- The release tag is account-restricted, protected, reachable from protected `main`, manually approved, and bound to reproducible verified assets.
- No live non-DNS mutation, secret exposure, unrelated change, or unsupported compatibility claim occurs.
