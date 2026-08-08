# UniFi CLI 1.0 Release-Candidate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring `codex/unifi-api-compatibility` to a safe, documented `v1.0.0-rc.1` targeting UniFi Network 10.3.58+.

**Architecture:** Stable commands use the official local `/proxy/network/integration/v1` API through a paginated typed client. Mutations are prepared against immutable IDs, centrally gated by risk and experimental status, applied once, and verified against controller-observed state.

**Tech Stack:** Go 1.26.5, Cobra, `net/http`, GoReleaser 2.17.1, GitHub Actions.

## Global Constraints

- Local controller API only; no UniFi cloud or Site Manager API.
- Every write requires `--yes`; `--dry-run` always wins.
- Experimental applies require `--experimental`; high-impact and destructive applies require `--force` when `safe_mode=true`.
- Stable compatibility floor is UniFi Network 10.3.58.
- API keys and WLAN secrets never enter argv, logs, reports, or JSON output.
- The current local network receives read-only tests plus only the isolated DNS A-record lifecycle.
- Publish `v1.0.0-rc.1`, not final `v1.0.0`.

---

### Task 1: Transport, origin, and TLS security

**Files:** `internal/config/config.go`, `internal/client/client.go`, `scripts/smoke.sh`, owning tests and example config.

- [ ] Write failing tests for malformed hosts, IPv4/IPv6 URL construction, port/timeout validation, custom CA loading, conflicting TLS settings, and 301/302/307/308 redirects.
- [ ] Validate a bare hostname/IP and port 1-65535; build the origin structurally.
- [ ] Add `ca_cert` and `UNIFI_CA_CERT`, rejecting combination with `insecure=true`.
- [ ] Reject all redirects before credentials or mutation bodies can be forwarded.
- [ ] Make verified TLS the live-smoke default and warn only on explicit insecure mode.
- [ ] Run focused config/client/auth tests and commit `fix: harden controller transport and TLS`.

### Task 2: Stable output, versioning, and mutation framework

**Files:** `internal/cli`, `internal/render`, `internal/plan`, new `internal/buildinfo`, and tests.

- [ ] Add failing tests for JSON schema version 1, version commands, removal of `--raw`, risk tiers, experimental gating, dry-run precedence, and target-binding races.
- [ ] Add `unifi version`, `version --json`, root `--version`, and linker-populated version/commit/build date.
- [ ] Freeze `{schema_version,ok,resource,action,data,meta,error?,plan?}` and remove raw payload support.
- [ ] Add `--experimental`, `RiskClass`, and prepared mutations bound to immutable target IDs and snapshots.
- [ ] Enforce routine/high-impact/destructive gates centrally and revalidate plan-relevant state before apply.
- [ ] Commit `feat: freeze v1 output and mutation contracts`.

### Task 3: Official API foundation and pagination

**Files:** `internal/client`, official client fixtures, and tests.

- [ ] Write failing tests for empty, single-page, multi-page, malformed, non-progressing, permission-denied, and ambiguous-site responses.
- [ ] Add typed pages and fetch at limit 100 until `offset + count >= totalCount`.
- [ ] Retain legacy unwrapping only for explicitly experimental legacy calls.
- [ ] Resolve sites by exact UUID, internal reference, or display name and cache the selected UUID per invocation.
- [ ] Escape dynamic path segments and construct queries with `url.Values`.
- [ ] Commit `feat: add paginated official Network API client`.

### Task 4: DNS correctness and stable mutation proof

**Files:** `internal/domain/dns.go`, `internal/cli/dns.go`, DNS tests and fixtures.

- [ ] Write failing tests for pagination, all documented DNS types, A-only mutation enforcement, input validation, no-op updates, missing IDs, post-write mismatch, and delete verification.
- [ ] Preserve type-specific normalized fields for A, AAAA, CNAME, MX, TXT, SRV, and forwarded-domain policies.
- [ ] Keep writes limited to A records and reject mutation of other types.
- [ ] Validate names, IPv4 addresses, and TTLs locally.
- [ ] Require returned IDs, re-read creates/updates, confirm deletes by ID and exact name, and never retry ambiguous mutations.
- [ ] Commit `fix: make DNS policies complete and type safe`.

### Task 5: Official reads and typed validation

**Files:** resource services under `internal/domain`, CLI wiring under `internal/cli`, fixtures and tests.

- [ ] Add failing official-API and golden-output tests for site, device, client, network, WLAN, port, firewall, DNS, and health reads.
- [ ] Migrate stable reads to official 10.3.58 endpoints and normalize into the existing snake_case DTO contract.
- [ ] Validate VLANs, CIDRs, IPs, DNS names, resolvers, port indexes, enums, and required inputs.
- [ ] Represent update fields explicitly, add applicable clear flags, and reject zero-field updates.
- [ ] Commit `feat: migrate stable resources to official Network API`.

### Task 6: Modern firewall policies and atomic reorder

**Files:** `internal/domain/firewall.go`, `internal/cli/firewall.go`, tests and official fixtures.

- [ ] Write failing tests for zone resolution, field preservation, no-op rejection, one-request atomic reorder, and final-order verification.
- [ ] Replace classic firewall reads with official zones and policies; add `firewall zone list/get`.
- [ ] Replace `--ruleset` with source/destination zone inputs and the official policy schema.
- [ ] Preserve the complete supported wire document when updating.
- [ ] Use the official zone-pair ordering endpoint and verify the complete final order.
- [ ] Keep firewall writes experimental and commit `feat: adopt modern firewall policy API`.

### Task 7: Remaining official and experimental mutations

**Files:** network, WLAN, device, client, port, resolver services and CLI tests.

- [ ] Add failing tests for official Network/WiFi CRUD, official device actions, legacy experimental gating, missing IDs, precondition changes, verification mismatches, and action acceptance.
- [ ] Migrate Network and WiFi CRUD plus restart/adopt/forget to official endpoints.
- [ ] Keep rename/locate/upgrade, client actions, port update, and resolver set as legacy experimental operations.
- [ ] Re-read creates/updates, verify deletes, and report action endpoints as accepted rather than completed.
- [ ] Commit `feat: verify and gate controller mutations`.

### Task 8: Documentation, trademark disclaimer, and repository metadata

**Files:** `README.md`, `SECURITY.md`, `CONTRIBUTING.md`, `CHANGELOG.md`, compatibility documentation, CLI long help.

- [ ] Add the prominent unofficial-project and Ubiquiti trademark disclaimer to README, CLI long help, and RC notes.
- [ ] Document the 10.3.58 floor, stable/experimental matrix, custom CA, site selectors, safety tiers, JSON schema, release installation, and verification.
- [ ] Update security support and contribution/live-lab policy.
- [ ] Set the post-merge GitHub description to `Unofficial CLI for safely managing local UniFi Network controllers. Not affiliated with or endorsed by Ubiquiti.`
- [ ] Commit `docs: define unofficial v1 release contract`.

### Task 9: Reproducible release pipeline and delivery

**Files:** `.goreleaser.yaml`, `.github/workflows/ci.yml`, new release workflow, release smoke scripts/tests.

- [ ] Add GoReleaser 2.17.1 builds for darwin/linux/windows on amd64/arm64, Windows zip and other tar.gz archives, SHA-256 checksums, SBOMs, source archive, and linker metadata.
- [ ] Add a tag workflow with the approved immutable GoReleaser, SBOM, and provenance action SHAs.
- [ ] Add required Linux race testing and smoke every produced binary.
- [ ] Run formatting, vet, unit, race, coverage, vulnerability, GoReleaser, cross-build, security, read-only live, and isolated DNS lifecycle gates.
- [ ] Rebase, push, open and merge the PR after hosted checks, tag the unchanged merge as `v1.0.0-rc.1`, verify downloaded artifacts and `go install`, then update the GitHub description.
- [ ] Do not create `v1.0.0` or live-test non-DNS mutations on the current controller.
