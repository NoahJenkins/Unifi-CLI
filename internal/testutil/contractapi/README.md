# API contract emulator

This test-only package runs the production CLI/client against an in-memory TLS
server. It contains independently written Go rules and synthetic data. It must
not be imported by production packages.

## Run

Ordinary tests need no external network or OpenAPI files:

```sh
go test ./internal/testutil/contractapi ./internal/cli
```

The explicit maintainer check first obtains the public source files, then runs
all contract cases with both validators:

```sh
./scripts/fetch-test-openapi.sh
UNIFI_CONTRACT_CACHE="$PWD/.cache/unifi-openapi" go test ./internal/testutil/contractapi ./internal/cli -count=1
```

Tests never download. A nonempty `UNIFI_CONTRACT_CACHE` requires both verified
files; missing, altered, or unsupported sources fail. The original cached files
remain unchanged and gitignored. Never stage them or upload them as artifacts.
The fetch script uses HTTPS and pins each source digest before installation.

## Profile and coverage manifest

Both explicit profiles use the initial subset below. They have separate pinned
sources in `profile.go`; tests run both even where the rules agree.

| Profile | Public source | SHA-256 |
| --- | --- | --- |
| 10.4.57 | https://developer.ui.com/network/v10.4.57/openapi.json | 3773947b4572b1bdc84272d3d2b9ebb5cad0d9aaa2640c3418568df97c9df59d |
| 10.6.109 | https://developer.ui.com/network/v10.6.109/openapi.json | 2356b910f76c175db9a22ce18070c2c40af02d9843ca67628410a5afbfc5086c |

| Resource | Operations under `/sites/{siteId}` | Offline branches and source components |
| --- | --- | --- |
| Firewall | GET `/firewall/policies`, GET/PUT/DELETE detail, POST collection | `Create or update firewall policy`, `Firewall policy`; ALLOW/BLOCK/REJECT; IPV4/IPV6/IPV4_AND_IPV6; TCP/UDP and applicable ICMP/ICMPV6, TCP_UDP preset, unrestricted; exact IP_ADDRESS/SUBNET source, IP_ADDRESS destination, PORT_NUMBER |
| Network | GET `/networks`, GET/PUT detail | `Create or update Network`, `Network details`; GATEWAY with IPv4 DHCP SERVER; SWITCH with DHCP omitted; UNMANAGED; rename, gateway mDNS, preservation of domain, DNS servers, range and lease fields |
| WLAN | GET `/wifi/broadcasts`, GET/PUT detail | `Wifi broadcast create or update`, `Wifi broadcast details`; STANDARD, OPEN security, SPECIFIC network; absent/null/DEVICES/DEVICE_TAGS AP filter; rename, network change, device selection and reset |

`rules.go` defines the exact modeled fields, required fields, bounds, enums,
array uniqueness, and UUID formats. An unmodeled object field or union branch
returns `CoverageError`, not evidence of an upstream rejection. The server marks
the test as failed and returns HTTP 500 with code `emulator_coverage_gap` for
these inputs. A negative CLI test cannot pass by mistaking missing emulator
coverage for a controller rejection. Modeled contract violations still return
HTTP 400; differently cased modeled discriminator keys remain invalid.
The shared
corpus compares acceptance within this subset. Cached validation additionally
checks all reachable published assertions and mappings. No upstream rule or
request builder is generated into the offline tables. The discriminator adapter
requires object containers even when a published base omits `type`. The only
null exception is the broadcasting-device filter: both pinned documents
explicitly describe null as all APs. Invalid scalar containers and allowed null
have separate regressions.

Excluded: personal/enterprise WLAN preservation and security transitions,
IOT_OPTIMIZED, other network DHCP/IP modes, management transitions, firewall
ordering, zone writes, other filter types, DNS, and legacy writes. The CLI can
preserve a seeded DEVICE_TAGS filter but has no tag-selection flag; the test
covers preservation and conversion to devices/all APs, not a new command.

## Reviewed correction

Cached results mean **published schema plus reviewed correction**, not strict
conformance to the unchanged source. In all three named-protocol components,
the pinned documents contain lowercase `name` enums and uppercase discriminator
keys with no overlap. The approved correction replaces only those three enums
in memory with their exact mapping keys, after verifying the original digest
and contradictory shape. No general case conversion occurs. Tests retain
proof of the original conflict, rejection of lowercase values, preservation of
unrelated assertions, and unchanged cache bytes. A new digest needs review.

## State, faults, and evidence

PUT replaces writable state. Tests compare final state with an independent
seed plus only the intended changes; a lost optional field cannot pass by
being echoed back. Responses retain independent server fields. Normal modeled
resource seeds/responses are validated before use. A response that cannot be
constructed under the profile fails without committing the write; this is an
emulator coverage limit, not a controller-behavior claim.

Gateway responses require `mdnsForwardingEnabled` in both published schemas.
The absent-response scenario is an explicit fault injected after validation;
ordinary seeds include the field. Other fault hooks exercise pre-apply drift,
read-back mismatch, and a stored write followed by malformed response JSON.
CLI assertions check exact mutation counts and no completion data on errors.

CLI tests run serially, clear controller environment settings, use temporary
config/profile/CA paths, and inject a dummy key through the existing client
factory. The factory refuses any other origin or CA. Production transport
already disables proxies and all redirects. No saved key or controller is used.

Passing results prove only the declared subset and modeled state transitions.
They cannot prove undocumented rules (such as the DNS SRV domain requirement),
real network/device effects, or live controller acceptance. Non-DNS writes
remain experimental. No sacrificial hardware or home-lab write is required.

## Maintenance

Every PR changing request builders, emulator behavior, rules, profiles, or seeds
requires the explicit cached check for both versions, plus repository gates.
Record the tested commit, command, source digests, and result without payloads.
An unavailable cache leaves that check incomplete. Investigate validator
agreement failures; do not revise both sides merely to match the CLI. Extend
this manifest before adding a new branch. Default hosted CI remains offline.
