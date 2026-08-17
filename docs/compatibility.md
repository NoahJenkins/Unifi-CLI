# Compatibility

## Supported target

`v1.0.0` targets the official local UniFi Network integration API in
**UniFi Network 10.4.57 and newer**. It connects directly to
`https://<host>:<port>/proxy/network/integration/v1`; it does not require the
UniFi cloud or Site Manager.

| UniFi Network version | RC status |
|---|---|
| Earlier than 10.4.57 | Unsupported; required official schemas, discriminators, or endpoints can be absent or incompatible |
| 10.4.57 | Compatibility floor and schema target; covered by official-schema fixtures, typed validation, local HTTP/TLS tests, and unit/smoke/race gates. An earlier candidate passed read-only live checks and one A-record lifecycle; final v1 qualification remains a release gate |
| Later than 10.4.57 | Intended by the `10.4.57+` contract, subject to upstream API compatibility and release-specific verification |

Live verification used the release-candidate executable from commit
`434492b613e730916b8b06adeea9dc49f3fd1518`. Every configured read-only check
passed; the DNS collection was the only successfully queried empty optional
resource. The single write test created a uniquely named disabled A record,
changed only its documentation-range address, deleted only its captured ID,
and confirmed exact-name absence and baseline restoration. No device, client,
port, WiFi, network, resolver, or firewall mutation was run. The controller's
self-signed certificate required the documented explicit `insecure: true`
compatibility setting for this local test; verified TLS remains the default.

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
| Rename/locate/upgrade, client actions and connected-client fixed-IP set/clear, port update, resolver set | Experimental legacy local compatibility paths; no sacrificial-controller live proof |
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
- Firewall uses modern zones/policies and complete atomic ordering; classic
  rulesets and partial order replacement are unsupported.
- Switching and RADIUS profiles are read-only.
- Accepted action output does not claim asynchronous completion.
- Fixed-IP set/clear supports currently connected clients only, infers the
  current network, and verifies stored reservation state. It does not create
  never-seen clients, select another network, renew DHCP leases, or reconnect a
  client.

## Reporting a compatibility issue

Include the CLI release/commit, OS/architecture, UniFi Network version,
controller model class, command name, and a fully redacted error code/message.
Never attach API keys, controller responses, reports, inventories, hostnames,
public IPs, or real identifiers. Use [SECURITY.md](../SECURITY.md) for anything
that may be security-sensitive.
