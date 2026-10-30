# API contract emulator for tests

Status: approved after adversarial review on 2026-10-30.

## Implementation discovery: upstream conflict, amendment approved

The first cached-schema test found a contradiction in both pinned documents.
Each of the three named-protocol components (IPv4, IPv6, and IPv4-and-IPv6)
declares uppercase discriminator mapping keys but a lowercase `name` enum.
The mapping keys and enum have zero overlap. Thus `TCP` fails the enum and
`tcp` fails discriminator dispatch. Strict unmodified-schema acceptance is
impossible for these branches. The original cached contract test failed for
this reason; the amendment below replaces strict unchanged-schema acceptance.

Amendment approved by the user: retain and verify the
original cache bytes, then apply an explicit in-memory correction to only the
`name` enum in these three components for each of the two pinned digests.
Use the exact mapping keys as that enum. Require the expected contradictory
source shape before applying the correction, and fail on any unreviewed source
change. Do not add general case conversion or remove other enum checks.

Keep separate tests proving the original contradiction and proving the
corrected contract accepts `TCP`, rejects `tcp`, and still enforces subtype
and unrelated field constraints. Label results as validation against the
published schema plus a reviewed correction; do not claim strict conformance
to the unchanged published document. This resolves an upstream inconsistency
for the emulator only and does not establish live controller acceptance.

## Purpose and scope

Catch invalid controller requests with a contract independent of the CLI's
request builders. Run the real CLI, domain services, and HTTP client against a
stateful `httptest` TLS server at `/proxy/network/integration/v1`.

Start with these operations, under both 10.4.57 and 10.6.109 profiles:

- Firewall policies: GET collection/detail, POST create, PUT update, DELETE.
  Test exact IPv4 address/subnet filters and every protocol variant the CLI
  writes. Include read-only firewall zones for target resolution.
- Networks: GET collection/detail and PUT update of seeded networks. Test
  rename and mDNS updates, and preservation of complete writable documents.
- WLANs: GET collection/detail and PUT update of seeded broadcasts. Test
  rename, network selection, and broadcasting-device selection. Use synthetic
  fixtures without stored passphrases; secret transitions are deferred.
- Minimal application info, sites, and other read dependencies required by
  those commands. No general controller simulation.

The firewall case runs plan, create, read-back verification, update,
reverification, delete, and absence verification. Network and WLAN cases start
from seeds and run plan, update, and verification. Their create/delete paths,
DNS, ordering, zone writes, and other resources are later work.

The initial seed matrix is explicit; command names alone do not define coverage:

| Resource | Seeds and transitions |
| --- | --- |
| Firewall | CLI protocol variants TCP, UDP, ICMP, ICMPV6, TCP_UDP, and unrestricted protocol in their supported IP scopes; exact TCP filters with address and subnet sources; create, rename/update, delete |
| Network | GATEWAY with IPv4 DHCP SERVER and populated optional nested DNS/domain/lease fields; SWITCH and UNMANAGED; rename each mode; mDNS true/false on GATEWAY, plus an explicit missing-response-field fault; reject mDNS on other modes |
| WLAN | STANDARD with OPEN security and SPECIFIC network selection; populated optional writable fields; rename and change network; AP filter absent/null, DEVICES, DEVICE_TAGS; preserve tags, select devices, and reset to all APs |

Personal and enterprise WLAN preservation, security transitions, IOT_OPTIMIZED,
other network DHCP/IP modes, and management-mode transitions are outside this
first matrix. A rename can resend those branches, so this is a coverage limit
even when no security or management flag changes. Keep their existing unit
tests. Add a coverage manifest per profile listing the modeled operations,
seed branches, rules, and exclusions. Expand it before adding a new scenario.

## Contract sources and alternatives

Recommendation: Go rule tables for required offline tests, plus an opt-in
OpenAPI-backed mode. No new external dependency.

| Option | Benefit | Cost or limit |
| --- | --- | --- |
| Reduced synthetic JSON Schema fixtures | Reuse the existing validator; easy to inspect | Another hand-maintained schema can repeat the CLI's error; excludes rules not transcribed |
| Gitignored cached OpenAPI only | Direct validation against a pinned official source | Ordinary tests cannot require a download; cache-only coverage would be absent on a fresh checkout |
| Go tables plus optional cached OpenAPI | Offline regression protection and a separate upstream contract check | Two validation levels; tables still require review and maintenance |

Commit only independently written Go rules, synthetic examples, and source
metadata. Do not commit OpenAPI documents, document excerpts, generated copies,
or controller material. The Go tables cover required fields, primitive types,
bounds, enums, nullability, and discriminator branches in the initial seed
matrix. Keep them separate from production DTOs and builders.
An unmodeled branch fails the test as unsupported coverage, rather than
silently passing. Do not invent an unknown-field restriction absent from the
official schema.

The optional mode reads both pinned documents from
`.cache/unifi-openapi/`, excluded by `.gitignore`. A separate download script
fetches only the two public Ubiquiti URLs. Tests never download. Explicit
opt-in with a missing, mismatched, or uncompileable document fails; ordinary
tests always run the Go-rule suite. Do not upload the cache or payloads as CI
artifacts. Keep cache contents out of staged files.

Cached mode is opt-in for ordinary development but is a required maintainer
check for every PR that changes request builders, emulator behavior, rules,
profiles, or seeds. It runs both versions, using the same positive and negative
corpus as the offline validator. Compare their verdicts within the declared
subset; fail on disagreement. Report an unmodeled branch as a coverage gap,
not as an upstream schema rejection. Contract-changing PRs record the command,
source digests, tested commit, and result without payloads. If the cache cannot
be obtained or verified, that check remains incomplete; do not silently skip
it. No network access is added to ordinary unit tests or default CI gates.

Record source URL, declared version, SHA-256, operation, and component names
for each profile. A source refresh is a reviewed change. These public sources
were inspected on 2026-10-30 and are stored only in the ignored local cache:

- [10.4.57](https://developer.ui.com/network/v10.4.57/openapi.json):
  `3773947b4572b1bdc84272d3d2b9ebb5cad0d9aaa2640c3418568df97c9df59d`
- [10.6.109](https://developer.ui.com/network/v10.6.109/openapi.json):
  `2356b910f76c175db9a22ce18070c2c40af02d9843ca67628410a5afbfc5086c`

Both declare OpenAPI 3.1.0 and the expected version. Their named-protocol
mappings accept `TCP`, not `tcp`. Do not infer that all other rules match.

Reuse `jsonschema/v6` for ordinary schema assertions. Resolve operation schemas
and local references, and enforce applicable formats. Add a separate
discriminator traversal over properties, items, references, and `allOf`:

1. Require object containers for discriminator dispatch, including when the
   published base omits `type`. Require a string key and reject unknown mapping
   keys. Permit null only for the AP filter, explicitly documented as all APs
   in both pinned sources.
2. Validate the selected subtype's ordinary assertions, including inherited
   base rules, then traverse its nested discriminator-bearing fields.
3. Track active dispatches by schema identity and instance location. When a
   subtype's `allOf` returns to its active base, suppress only the repeated
   dispatch. Do not skip ordinary assertions or another instance's validation.

The published schemas use this base/subtype inheritance cycle. Plain JSON
Schema validation ignores discriminator mappings. The library's
[example](https://github.com/santhosh-tekuri/jsonschema/blob/v6.0.3/example_vocab_openpapi_discriminator_test.go)
also accepts unknown mapping keys and must not be used unchanged. Require
positive inherited-branch tests and negative missing-key, unknown-key,
wrong-type, nested-discriminator, and missing-subtype-field tests before
resource handlers. Test cycle termination without weakening either base or
subtype rules. Reject unresolved references and unsupported validation
features; harmless annotations are not validation features. Disable external
reference loading. Map documented `/v1` paths to the local proxy prefix.

## Package and test structure

- `internal/testutil/contractapi/`: server, locked per-test state, explicit
  version profiles, validators, and synthetic seeds. No production imports
  from domain or CLI packages. Production code must not import this package.
- `internal/cli/*_contract_test.go`: run commands through the existing test
  runtime with an isolated configuration and a dummy credential. Use the real
  client transport. Matrix the same scenarios across both profiles.
- `scripts/fetch-test-openapi.sh`: explicit public-source cache population.

CLI matrix cases run serially because the current runtime uses package-global
flags and process environment. Save and restore all relevant flags; clear
ambient connection, profile, credential, TLS, and safe-mode overrides. Use
temporary configuration/profile paths, explicit `safe_mode=true`, a dummy
credential, and the test server's certificate. Do not access the user's saved
credentials or CA files. Limit transport connections to the test server's
loopback address and port, including redirects; disable proxy routing. Locked
server state does not make the CLI helpers safe for `t.Parallel`.

Use deterministic synthetic IDs, paginated reads, documented success statuses,
and independent stored response documents. Validate before changing state.
PUT replaces the writable document; it must not merge omitted fields back
from the old object. Keep server-owned fields separate. Validate seeds and
normal responses too, so a permissive echo cannot hide missing requirements.
Unknown routes or methods fail tests; legacy routes are forbidden.

Schema validation and CLI read-back are insufficient preservation checks.
Each update test independently computes expected state from an immutable seed
plus the explicitly requested field changes, without using the request body
or production normalization helpers. Compare final writable state against that
expectation, including populated optional nested fields. Include a deliberate
optional-field omission that passes the request schema but fails this
preservation assertion. Assert server-owned identity fields separately.

Test the validator directly: lowercase `tcp`, wrong branch fields, missing
required fields, bad bounds, and malformed JSON must fail with unchanged
state. Test the CLI with explicit drift and read-back mismatch hooks, plus
dry-run and apply-gate checks. Add a deterministic fault that stores a valid
write and then loses or corrupts its response. Assert exactly one mutation
attempt, retained state, and error output without a completion claim. Count
mutation attempts in verification-failure cases too; dry-run, denied gates,
and pre-apply drift must send none. Faults run only on the test server.
Diagnostics contain rule names and JSON paths,
never request bodies or secret values. Keep existing focused unit tests.

## Evidence and limits

Offline results prove conformance to the committed subset and the modeled
state transitions. Cached-mode results additionally prove tested requests
meet the pinned published schemas plus the reviewed enum correction and
explicit discriminator handling described above. Neither
proves live controller acceptance or complete API coverage.

The emulator cannot detect undocumented rules such as the SRV base-domain
requirement. It cannot prove traffic filtering, device actions, WiFi behavior,
DHCP leases, timing, or other real effects. Legacy `rest/user` fixed-IP writes
have no official schema and remain outside this emulator. Non-DNS writes
remain experimental. The 10.5.67 failures remain unconfirmed; emulator success
does not establish their cause.

No controller access, credential retrieval, hardware purchase, or live writes
are needed. Never run non-DNS writes on the home-lab gateway.

## Proposed delivery

Delivery update: after implementation and review, the user approved one draft
PR for the complete change. The original sizing breakdown below describes
logical sections within that PR.

1. Contract foundation: coverage manifests, pinned cache script, provenance,
   cycle-safe schema adapter, offline rules, and validator agreement tests for
   the matrix. Roughly 1,000–1,600 added lines.
2. Stateful server and firewall lifecycle: independent state assertions,
   isolated CLI runtime, gates, drift, and ambiguous-write faults.
   Roughly 700–1,100 added lines.
3. Network/WLAN update scenarios: seed-derived preservation checks, matrix
   transitions, and failure cases. Roughly 600–1,000 added lines.

Total: three focused PRs, roughly 2,300–3,700 added lines including tests and
documentation. This is a planning range, not a limit. Confirm the estimate
after PR 1 inventories the needed schema rules; do not weaken validation to
fit it. All three PRs are required to complete this design.

Each implementation PR runs smoke, race, coverage, pinned govulncheck, and
the cached contract checks for both versions. The recurring maintainer check
above also applies after delivery. Acceptance requires rejection of the
original lowercase-protocol request by both validators under both profiles,
cycle-safe positive validation, independent preservation assertions, and
successful stateful scenarios with exact mutation counts. New contract
failures must be investigated, not removed to match current CLI behavior.
