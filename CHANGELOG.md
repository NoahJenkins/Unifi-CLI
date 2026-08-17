# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Experimental high-impact `client fixed-ip set` and `client fixed-ip clear`
  commands for currently connected clients. The legacy local write path uses
  plan/apply gates, subnet and conflict validation, immutable target checks,
  one non-retried write, and controller-state verification. It does not renew
  the DHCP lease or reconnect the client.

## [1.0.0-rc.1] - 2026-08-07

### Added

- Official local UniFi Network 10.3.58 integration client with strict
  pagination, exact site UUID/`internalReference`/display-name resolution, and
  per-invocation site caching.
- Schema-v1 JSON envelopes and linker-populated `unifi version`,
  `version --json`, and root `--version` interfaces.
- Custom controller CA support through `ca_cert` and `UNIFI_CA_CERT`.
- Modern firewall zones, policies, and atomic complete-order replacement.
- Explicit mutation risk classes and experimental opt-in.

### Changed

- Stable site, device, client, network, WiFi, port, firewall, DNS, resolver,
  and derived-health reads use the official local integration API.
- Network and WiFi CRUD plus supported device lifecycle actions use official
  endpoints. Creates capture returned IDs and verify controller-observed state;
  operations on pre-existing targets bind and revalidate immutable identities
  and snapshots before apply.
- Network writes use `--management gateway|switch|unmanaged`; the legacy
  `--purpose` write vocabulary is removed.
- All non-DNS writes are experimental for this RC. Unsupported official create
  variants fail closed instead of inventing controller-required fields.
- API-key persistence is native-keyring first, with an explicit protected-file
  fallback for headless systems.

### Security

- TLS verification is the default; custom CA and insecure mode conflict.
- Controller origins are structurally constructed from validated bare
  host/port values, and automatic redirects are rejected.
- Every write is plan-first, requires `--yes`, and respects dry-run precedence.
  Operations on pre-existing targets revalidate immutable identities and
  snapshots before one apply attempt. Creates validate input, write once
  without retry, capture returned IDs, and verify controller-observed state.
- High-impact and destructive writes require `--force` while `safe_mode` is
  enabled. API keys and WLAN passphrases remain out of argv and output.

### Stable release contract

- Compatibility floor: UniFi Network 10.3.58.
- Stable writes: official local DNS A-record create/update/delete only.
- Experimental official writes: Network/WiFi CRUD, supported device lifecycle,
  and modern firewall policy mutations.
- Experimental legacy compatibility writes: device rename/locate/upgrade,
  client actions, switch-port update, and resolver set.
- `data` is always present in schema-v1 JSON; `error` and `plan` are emitted
  only when applicable. Upstream raw payload output is not part of v1.

Fresh live compatibility and release-artifact verification remain delivery
gates; see [the compatibility document](docs/compatibility.md) and
[`v1.0.0-rc.1` release notes](docs/releases/v1.0.0-rc.1.md).
