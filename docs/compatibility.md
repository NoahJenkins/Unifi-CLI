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
| 10.4.57 | Live verified on 2026-08-08: the guarded read-only suite passed, system health reported the official application version, and the isolated disabled DNS A-record create/update/delete lifecycle restored the exact baseline |
| Later than 10.4.57 | Intended by the `10.3.58+` contract, subject to upstream API compatibility and release-specific verification |

Live verification used the release-candidate executable from commit
`434492b613e730916b8b06adeea9dc49f3fd1518`. Every configured read-only check
passed; the DNS collection was the only successfully queried empty optional
resource. The single write test created a uniquely named disabled A record,
changed only its documentation-range address, deleted only its captured ID,
and confirmed exact-name absence and baseline restoration. No device, client,
port, WiFi, network, resolver, or firewall mutation was run. The controller's
self-signed certificate required the documented explicit `insecure: true`
compatibility setting for this local test; verified TLS remains the default.

The schema target is Ubiquiti's [UniFi Network 10.3.58 API
reference](https://developer.ui.com/network/v10.3.58). Upstream documentation
also exposes an [OpenAPI document](https://developer.ui.com/network/v10.3.58/openapi.json)
used to derive synthetic fixtures and validation bounds.

## Surface compatibility

| Surface | API and support |
|---|---|
| Site/device/client/network/WiFi/port/firewall/DNS/resolver/health reads | Stable official local integration API; health combines official application info with adopted-device status |
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
