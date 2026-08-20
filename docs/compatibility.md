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
| 10.5.67 | Final v1 candidate passed all 19 configured read-only checks, with the empty DNS collection as one allowed `not_configured` result, and completed all seven disabled DNS policy lifecycles with exact baseline restoration |
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

The schema target is Ubiquiti's [UniFi Network 10.4.57 API
reference](https://developer.ui.com/network/v10.4.57). Upstream documentation
also exposes an [OpenAPI document](https://developer.ui.com/network/v10.4.57/openapi.json)
used to derive synthetic fixtures and validation bounds.

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
- DNS type changes are unsupported; update preserves the existing policy type.
- SRV `--name` is the base domain; `--service` and `--protocol` provide the
  underscore-prefixed SRV labels separately.
- Firewall uses modern zones/policies and complete atomic ordering; classic
  rulesets and partial order replacement are unsupported.
- Switching and RADIUS profiles are read-only.
- Accepted action output does not claim asynchronous completion.
- Fixed-IP list returns enabled reservations only. Get can report disabled
  known-client state but hides an inactive historical address.
- Fixed-IP set/clear supports known offline clients only when the legacy user
  record supplies an immutable ID, valid MAC, and stored network ID. It does
  not create never-seen clients, select another network, renew DHCP leases, or
  reconnect a client.
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
