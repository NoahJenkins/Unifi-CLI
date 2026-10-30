# Compatibility

## Supported target

`v1.0.0` targets the official local UniFi Network integration API in
**UniFi Network 10.4.57 and newer**. It connects directly to
`https://<host>:<port>/proxy/network/integration/v1`; it does not require the
UniFi cloud or Site Manager.

| UniFi Network version | RC status |
|---|---|
| Earlier than 10.4.57 | Unsupported; required official schemas, discriminators, or endpoints can be absent or incompatible |
| 10.4.57 | Compatibility floor and schema target; covered by official-schema fixtures, typed validation, local HTTP/TLS tests, and unit/smoke/race gates. An earlier candidate passed read-only live checks and one A-record lifecycle |
| 10.5.67 | Final v1 candidate passed all 19 configured read-only checks, with the empty DNS collection as one allowed `not_configured` result, and completed all seven disabled DNS policy lifecycles with exact baseline restoration. A later exact-filter firewall create request was rejected before creation; see the limitation below |
| 10.6.109 | An operator-reported exact-filter firewall create returned `api.request.unknown-type-id` before creation. The published 10.6.109 protocol schemas match 10.4.57; the named-protocol casing fix below targets this rejection. Other surfaces have no recorded qualification on this version |
| Later than 10.5.67 | Intended by the `10.4.57+` contract, subject to upstream API compatibility and release-specific verification |

Final live verification used a release-candidate executable from this branch.
Every configured read-only check passed or returned the allowed
`not_configured` result. The write gate sequentially created, verified,
updated, reverified, and deleted uniquely named disabled A, AAAA, CNAME, MX,
TXT, SRV, and forwarded-domain policies. It deleted only captured IDs and
restored the exact empty baseline. Qualification found that SRV requires a base
`domain` with separate `_service` and `_protocol` fields; the CLI regression
was fixed and tested before release. No device, client, port, WiFi, network,
resolver, firewall, or fixed-IP mutation was run. The controller's self-signed
certificate required the documented explicit `insecure: true` compatibility
setting for this local test; verified TLS remains the default.

Post-publication RC.2 installation verification found that tagged source
installs reported `dev` only from the short root `--version` flag. RC.3 fixes
that build-information path. It does not change controller API behavior or the
live qualification conclusions above.

RC.4 changes release documentation only. It corrects the stable release notes
to identify the exact candidate used for unchanged-source promotion. It does
not change controller or executable behavior.

After the exact-filter firewall surface merged, two operator-reported 10.5.67
apply attempts returned HTTP 400 before policy creation. Read-only before/after
checks found no added or removed policy IDs. The published 10.4.57 OpenAPI
request schema matches the CLI request document, including the integer
`PORT_NUMBER`.
As of 2026-08-29, Ubiquiti does not publish a 10.5.67 OpenAPI document, and the
earlier CLI discarded the documented validation fields. Therefore there is not
enough primary-source evidence to change the request body. The CLI now exposes
only bounded, sanitized official validation `code` and `message` fields so a
future authorized sacrificial-controller attempt can identify the difference.
No controller request or live mutation was performed for this follow-up.

Later, an operator-reported 10.6.109 exact-filter create returned
`api.request.unknown-type-id` for `$.ipProtocolScope.protocolFilter.type`
before creation. Read-only checks found no new policy. The published 10.4.57
and 10.6.109 OpenAPI documents define the named protocol as a discriminator
whose mapping keys are uppercase (`TCP`, `UDP`, `ICMP`, `ICMPV6`, and others).
Earlier builds sent the lowercase CLI value (`tcp`), which is not a mapping
key, so the earlier statement that the request matched the schema was wrong.
Every other discriminator in the exact-filter request matches the published
mapping keys. The CLI now sends the uppercase names. The controller reported
the parent `type` path, so this cause is supported by the schema but is not yet
confirmed by a successful live create. For `unknown-type-id` only, the CLI now
keeps the rejected single-case schema token in the sanitized message.

The schema target is Ubiquiti's [UniFi Network 10.4.57 API
reference](https://developer.ui.com/network/v10.4.57). Upstream documentation
also exposes an [OpenAPI document](https://developer.ui.com/network/v10.4.57/openapi.json)
used to derive synthetic fixtures and validation bounds.

## Client platforms

Release archives target darwin, linux, and windows on amd64 and arm64. The
CLI is built with the Go version declared in `go.mod`. From Go 1.27, macOS
binaries require macOS 13 Ventura or later.

## Surface compatibility

| Surface | API and support |
|---|---|
| Site/device/client/network/WiFi/port/firewall/DNS/resolver/health reads | Stable official local integration API; health combines official application info with adopted-device status |
| LAG, MC-LAG, switch-stack, RADIUS-profile, and traffic-list reads | Stable official local integration API |
| All seven official DNS policy create/update/delete variants | Stable official local integration API |
| Network/WiFi CRUD, restart/adopt/forget, firewall policy/zone/traffic-list writes | Experimental official local integration API; no sacrificial-controller live proof |
| Rename/locate/upgrade, client actions, known-client fixed-IP list/get/set/clear, port update, resolver set | Experimental legacy local compatibility paths; fixed-IP inventory has read-only Home Lab proof, but no sacrificial-controller write proof |
| Named profiles and `doctor` | Local-only configuration and readiness surface; no controller request from `doctor` |
| Classic firewall and switching/RADIUS writes | Unsupported |

Reads preserve the schema-v1 CLI contract rather than exposing raw upstream
documents. Official collection pages are validated strictly. Required detail
fan-out for unfiltered port and resolver lists is capped at four workers and
fails the entire command when any required detail fails.

## Known intentional limits

- Network management transitions require every target-mode field; DHCP ranges
  and controller defaults are never inferred.
- Personal WiFi secrets are accepted only by hidden prompt or bounded stdin.
- Per-WLAN access-point broadcasting selection uses the official
  `broadcastingDeviceFilter` representation and remains an experimental WiFi
  update without sacrificial-controller live qualification.
- DNS type changes are unsupported; update preserves the existing policy type.
- SRV `--name` is the base domain; `--service` and `--protocol` provide the
  underscore-prefixed SRV labels separately.
- Firewall uses modern zones/policies and complete atomic ordering; classic
  rulesets and partial order replacement are unsupported.
- Firewall policy create supports only one exact source IPv4 address or one
  canonical source IPv4 subnet (`/8` through `/32`), one exact destination
  IPv4 address, and one exact TCP destination port when address filters are
  requested. The source, destination, and port filters are an all-or-none
  bundle with action allow and return traffic enabled. The source subnet uses
  the official 10.4.57 `SUBNET` IP-matching item. Destination subnets, ranges,
  matching lists, MACs, port ranges, source ports, and connection-state
  controls remain unsupported create surfaces.
- Exact firewall filter create is derived from the official 10.4.57 OpenAPI
  schema and is covered by reduced synthetic fixtures and repository tests.
  Network 10.5.67 and 10.6.109 rejected operator-reported attempts before
  creation. Those builds sent lowercase named-protocol discriminators; the
  current build sends the official uppercase names. A successful live create
  has not yet confirmed this fix. It remains without sacrificial-controller
  live qualification. The
  source-subnet variant has the same status: it is derived from the official
  10.4.57 schema, it has no 10.5.67 schema evidence, and no live write was run.
- Switching and RADIUS profiles are read-only.
- Accepted action output does not claim asynchronous completion.
- Fixed-IP list returns enabled reservations only. Get can report disabled
  known-client state but hides an inactive historical address.
- Fixed-IP set/clear supports known offline clients only when the legacy user
  record supplies an immutable ID and valid MAC. Clear also requires a stored
  network ID. When the record has no network ID, set requires an explicit
  `--network`; the CLI does not infer one. A connected client must currently
  use the selected network and subnet. A stored network ID cannot be replaced
  by a different `--network`. Set does not create never-seen clients, renew
  DHCP leases, or reconnect a client.
- Fixed-IP writes stay on the legacy `rest/user` endpoint. The official 10.4.57
  OpenAPI exposes client reads and guest-access actions only; it has no client
  DHCP reservation write. No live fixed-IP write has been run for the
  missing-network-ID path.
- Network mDNS forwarding updates use the official optional
  `mdnsForwardingEnabled` field. It applies only to gateway-managed networks,
  because the official switch-managed and unmanaged documents do not define it.
  An absent value means the site default; the CLI can set it to true or false
  but cannot reset it to the site default. No live network write has been run.
- `network list` reads every official network detail to report subnet, DHCP,
  domain, and mDNS state. It fails as a whole if any detail read fails.
- Fixed-IP conflict checks cover enabled reservations and current connected
  addresses. They cannot detect an offline device that uses an unrecorded
  static address.
- Named profiles contain non-secret connection settings only. `doctor` proves
  local config and credential-source readiness; `auth status` remains the
  online reachability and credential-validity check.

## Reporting a compatibility issue

Include the CLI release/commit, OS/architecture, UniFi Network version,
controller model class, command name, and a fully redacted error code/message.
Never attach API keys, controller responses, reports, inventories, hostnames,
public IPs, or real identifiers. Use [SECURITY.md](../SECURITY.md) for anything
that may be security-sensitive.
