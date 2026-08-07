# Compatibility

## Supported target

`v1.0.0-rc.1` targets the official local UniFi Network integration API in
**UniFi Network 10.3.58 and newer**. It connects directly to
`https://<host>:<port>/proxy/network/integration/v1`; it does not require the
UniFi cloud or Site Manager.

| UniFi Network version | RC status |
|---|---|
| Earlier than 10.3.58 | Unsupported; required official schemas/endpoints may be absent or incompatible |
| 10.3.58 | Compatibility floor and schema target; covered by official-schema fixtures, typed validation, local HTTP/TLS tests, and unit/smoke/race gates |
| 10.4.57 | In the supported target range, but this RC branch has not yet completed fresh live-controller verification; do not treat compatibility as proven until the Task 9 release gate records it |
| Later than 10.4.57 | Intended by the `10.3.58+` contract, subject to upstream API compatibility and release-specific verification |

The implementation work for this RC intentionally made no live-controller
calls. No fresh live verification of the rewritten official-API RC surface on
10.4.57 has been recorded. The release checklist requires fresh read-only
coverage and only the approved isolated DNS A-record lifecycle before
publication.

The schema target is Ubiquiti's [UniFi Network 10.3.58 API
reference](https://developer.ui.com/network/v10.3.58). Upstream documentation
also exposes an [OpenAPI document](https://developer.ui.com/network/v10.3.58/openapi.json)
used to derive synthetic fixtures and validation bounds.

## Surface compatibility

| Surface | API and support |
|---|---|
| Site/device/client/network/WiFi/port/firewall/DNS/resolver/health reads | Stable official local integration API |
| DNS A-record create/update/delete | Stable official local integration API |
| Network/WiFi CRUD, restart/adopt/forget, firewall policy writes | Experimental official local integration API |
| Rename/locate/upgrade, client actions, port update, resolver set | Experimental legacy local compatibility paths |

Reads preserve the schema-v1 CLI contract rather than exposing raw upstream
documents. Official collection pages are validated strictly. Required detail
fan-out for unfiltered port and resolver lists is capped at four workers and
fails the entire command when any required detail fails.

## Known intentional limits

- Network create requires `--management`. UNMANAGED, SWITCH, and GATEWAY
  variants fail closed where required inputs are not exposed; management-mode
  transitions are rejected.
- WiFi create supports OPEN and WPA2 Personal. WPA3 Personal, mixed WPA2/WPA3,
  and enterprise creation are rejected because required SAE/PMF/fast-roaming
  or RADIUS inputs are not exposed.
- DNS writes are A-record only even though reads normalize all official policy
  types.
- Firewall uses modern zones/policies and complete atomic ordering; classic
  rulesets and partial/per-policy reorder are unsupported.
- Accepted action output does not claim asynchronous completion.

## Reporting a compatibility issue

Include the CLI release/commit, OS/architecture, UniFi Network version,
controller model class, command name, and a fully redacted error code/message.
Never attach API keys, controller responses, reports, inventories, hostnames,
public IPs, or real identifiers. Use [SECURITY.md](../SECURITY.md) for anything
that may be security-sensitive.
