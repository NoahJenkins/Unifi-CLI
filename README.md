# unifi-cli

> **Unofficial project.** unifi-cli is an independent community tool and is not affiliated with, endorsed by, or sponsored by Ubiquiti Inc. UniFi is a trademark of Ubiquiti Inc.

Neither the author nor users of this project own the UniFi brand; Ubiquiti Inc. owns it.

Manage a **local** UniFi Network controller from the terminal. The
The v1 release uses the official local Network integration API
for its stable surface, emits a versioned JSON contract for automation, and
plans every mutation before it can apply.

> **Local controller only.** This CLI talks directly to the on-network UniFi
> Network Application or Cloud Gateway. It does not use UniFi Site Manager,
> UniFi cloud APIs, or the remote Connector examples shown in the upstream API
> reference.

The compatibility target is UniFi Network **10.4.57 and newer**. The current
implementation is schema- and fixture-validated against the official 10.4.57
API. An earlier candidate completed the guarded read-only suite and an isolated
DNS A-record lifecycle on 10.4.57. The expanded v1 surface still requires final
read-only and seven-type DNS qualification before publication. See
[Compatibility](docs/compatibility.md) for the exact status and limits.

## Install v1

After the stable tag is published:

```bash
go install github.com/noahjenkins/unifi-cli/cmd/unifi@v1.0.0
unifi --version
unifi version --json
```

Release archives are also attached to each GitHub release. Before publication,
build the reviewed source checkout with the Go
version declared in `go.mod`:

```bash
go build -o dist/unifi ./cmd/unifi
./dist/unifi --help
./dist/unifi version
```

Do not treat a locally built `dev` version as a published release. Release builds
populate the version, commit, and build date through linker metadata.
Tagged `go install` builds report the tag version, but their `commit` and
`build_date` remain `unknown`. Use a verified release archive when you need
authoritative full build metadata, checksums, CycloneDX SBOMs, and provenance.

## Quick start

Copy and edit the non-secret controller configuration:

```bash
mkdir -p ~/.config/unifi-cli
cp configs/config.example.yaml ~/.config/unifi-cli/config.yaml
$EDITOR ~/.config/unifi-cli/config.yaml
```

```yaml
host: controller.lan
port: 443
# ca_cert: /path/to/controller-ca.pem
insecure: false
site: default
safe_mode: true
timeout: 30s
```

Then save an API key and verify read access:

```bash
unifi login
unifi auth status --json
unifi site list
unifi system health --json
```

`unifi login` reads the key from a hidden prompt, validates it with the
controller, and saves it in the native credential store:

- macOS: Keychain
- Windows: Credential Manager
- Linux: Secret Service, such as GNOME Keyring or KWallet

On headless Linux without Secret Service, `unifi login --file-fallback`
explicitly opts in to a protected local-state file. The fallback is never
automatic. `UNIFI_API_KEY` is a process-only override for CI or scripts and is
not persisted. `unifi logout` removes only the saved local key for the selected
controller; it does not call a remote logout endpoint.

API keys never belong in YAML, command arguments, logs, reports, or issue
attachments.

## Controller configuration

The default path is `~/.config/unifi-cli/config.yaml`; override it with
`--config` or `UNIFI_CONFIG`.

| YAML | Environment | Contract |
|---|---|---|
| `host` | `UNIFI_HOST` | Required bare hostname, IPv4, or IPv6 address; no scheme, path, query, userinfo, brackets, whitespace, or embedded port |
| `port` | `UNIFI_PORT` | Integer `1..65535`; default `443` |
| `ca_cert` | `UNIFI_CA_CERT` | PEM file appended to system roots for verified private-CA TLS |
| `insecure` | `UNIFI_INSECURE` | Explicit boolean TLS-verification bypass; default `false` |
| `site` | `UNIFI_SITE` | Exact site UUID, exact `internalReference`, or exact display name |
| `safe_mode` | `UNIFI_SAFE_MODE` | Requires `--force` for high-impact and destructive applies; default `true` |
| `timeout` | `UNIFI_TIMEOUT` | Positive Go duration such as `30s` |

`--site` and `--timeout` override the loaded values for one invocation.
Boolean environment values are parsed strictly.

### TLS

TLS certificate verification is enabled by default. Prefer `ca_cert` or
`UNIFI_CA_CERT` for a controller signed by a private CA. `ca_cert` and
`insecure: true` conflict and fail configuration loading; the same applies to
`UNIFI_CA_CERT` with `UNIFI_INSECURE=true`.

`insecure: true` is a last-resort trusted-LAN compatibility option. Traffic is
encrypted but the controller is not authenticated, so an active network
attacker can impersonate it and capture the API key. The client also rejects
all HTTP redirects so credentials and mutation bodies cannot be forwarded to a
different origin.

### Site resolution

The configured site selector must exactly match a site UUID,
`internalReference`, or display name. Distinct matching UUIDs fail with
`ambiguous_id`; there is no fuzzy match. The resolved UUID is cached for the
remainder of that CLI invocation and is used in official API paths.

## Commands and support status

All controller inventory and health reads below use the official local
`/proxy/network/integration/v1` API.
“Experimental” means a plan can be inspected normally, but applying it also
requires `--experimental`.

| Surface | Status | Commands |
|---|---|---|
| Local auth/config/version | Local | `login`, `logout`, `auth status`, `config path`, `config show`, `version` |
| Inventory and health reads | Stable official | `site`, `device`, `client`, `network`, `wlan`, `port`, `firewall`, `firewall zone`, `firewall traffic-list`, `dns`, `dns resolvers`, `switching lag`, `switching mc-lag`, `switching stack`, `radius profile`, and `system health` list/get operations as exposed by help |
| Local DNS policy writes | Stable official | `dns create`, `dns update`, `dns delete` for A, AAAA, CNAME, MX, TXT, SRV, and forwarded-domain policies |
| Network and WiFi writes | Experimental official | Network CRUD; WLAN CRUD, enable, and disable |
| Device lifecycle writes | Experimental official | `device restart`, `device adopt`, `device forget` |
| Firewall writes | Experimental official | Policy create/update/delete/reorder, relative policy move, custom-zone CRUD, and traffic-list CRUD |
| Legacy device/client/port/resolver writes | Experimental legacy | Device rename/locate/upgrade; client reconnect/block/unblock and fixed-IP set/clear; port update; resolver set |
| Unsupported | None | Classic firewall rulesets; switching LAG, MC-LAG, and stack writes; RADIUS profile writes |

The legacy-experimental rows are deliberately isolated compatibility paths;
they are not described as stable official API support. There are no stable
non-DNS mutations in this RC.

## Mutation safety

Every mutation validates input and emits a plan. It applies only when `--yes`
is present. `--dry-run` always wins over `--yes`. Experimental plans do not
need opt-in, but experimental applies require both `--experimental` and
`--yes`. With `safe_mode: true`, high-impact and destructive applies also
require `--force`. `--force` never implies `--yes` or `--experimental`.

Targeted operations—updates, deletes, actions, and reorder operations that
first observe existing target state—bind the plan to an immutable ID or state
identity and observed snapshot. They revalidate immediately before one apply
attempt and fail on drift.

Creates have no pre-existing target to revalidate. DNS, Network, WLAN, and
firewall creates validate their inputs locally, emit an untargeted plan, make
at most one non-retried write, require the controller to return an ID, and then
re-read controller state to verify the requested result. No mutation retries
an ambiguous write.

### Exact risk and support classification

| Risk | Support/API | Commands | Apply flags with default `safe_mode` |
|---|---|---|---|
| `routine` | Stable official | `dns create`, `dns update` | `--yes` |
| `routine` | Experimental official | `device adopt`; `network create`; `wlan create/update/enable/disable` | `--experimental --yes` |
| `routine` | Experimental legacy | `device rename/locate`; `client reconnect/block/unblock` | `--experimental --yes` |
| `high_impact` | Experimental official | `device restart`; `network update`; `firewall create/update/reorder/move`; `firewall zone create/update`; `firewall traffic-list create/update` | `--experimental --force --yes` |
| `high_impact` | Experimental legacy | `device upgrade`; `client fixed-ip set/clear`; `port update`; `dns resolvers set` | `--experimental --force --yes` |
| `destructive` | Stable official | `dns delete` | `--force --yes` |
| `destructive` | Experimental official | `device forget`; `network delete`; `wlan delete`; `firewall delete`; `firewall zone delete`; `firewall traffic-list delete` | `--experimental --force --yes` |

If `safe_mode` is explicitly disabled, `--force` is not required; all other
gates remain. Successful applies emit `audit: applied <resource> <action>` on
stderr unless `--quiet` is set.

```bash
# Plan only: no write
unifi network update LAN --name Users

# Experimental high-impact apply
unifi network update LAN --name Users --experimental --force --yes

# Explicit dry-run still wins
unifi network update LAN --name Users --experimental --force --yes --dry-run
```

## Resource contracts and limits

### DNS

Reads normalize official A, AAAA, CNAME, MX, TXT, SRV, and forwarded-domain
policies. Stable create accepts `--type a|aaaa|cname|mx|txt|srv|forward-domain`
and defaults to `a`. Type-specific flags are validated strictly; update infers
and preserves the existing type and rejects type changes. Create and update
re-read the captured ID and compare the complete expected policy. Delete
verifies the captured ID is absent and does not accept a different same-name
policy as proof of deletion.

### Networks

Network writes use the official `--management gateway|switch|unmanaged`
discriminator. The removed legacy `--purpose` vocabulary (`corporate`,
`guest`, `wan`) is not accepted for official writes.

Creation needs an explicit VLAN ID in `1..4009`. `gateway`, `switch`, and
`unmanaged` have distinct official documents. Switch management requires
`--device`. Gateway DHCP uses explicit `--dhcp-mode none|server|relay` fields.
Server mode requires a complete range and lease value when the target mode
cannot preserve them; relay mode requires one or more relay addresses. The CLI
never invents a DHCP range. A management transition is accepted only when all
required target-mode fields are present. Updates preserve and send the complete
writable official document.

### Client fixed-IP reservations

Fixed-IP reservations use the legacy local client configuration endpoint
because the official integration API does not expose this write. The commands
operate only on currently connected clients:

```bash
# Plan only
unifi client fixed-ip set Laptop 192.168.1.50

# Experimental high-impact apply
unifi client fixed-ip set Laptop 192.168.1.50 --experimental --force --yes

# Disable the reservation; this does not reconnect the client
unifi client fixed-ip clear Laptop --experimental --force --yes
```

The CLI infers the client's current network. Set requires DHCP to be enabled
and a usable IPv4 address inside that network's subnet. It rejects the network,
broadcast, and gateway addresses, another enabled reservation, and an address
currently used by another connected client. It does not require the address to
be inside the dynamic DHCP pool.

Set and clear verify the stored controller configuration after one write and
do not retry ambiguous writes. Success does not mean the client renewed its
DHCP lease or changed its active address. Use the separate experimental
`client reconnect` action when an explicit reconnect is appropriate.

### WiFi

Official create and update support OPEN, WPA2 Personal, WPA3 Personal, mixed
WPA2/WPA3 Personal, WPA2 Enterprise, mixed WPA2/WPA3 Enterprise, and WPA3
Enterprise STANDARD broadcasts. The typed flags cover PMF, SAE timers, fast
roaming, RADIUS profile and NAS ID, Change of Authorization, and WPA3 enterprise
security mode. Personal passphrases must be 8–63 characters and enter through
the hidden `--password` prompt or bounded `--password-stdin`, never as an
argument value or plan field. Updates preserve the complete official security
document when a field is not changed.

### Firewall

Firewall reads and writes use modern official zones and policies; classic
rulesets are not supported. Resolve zones with `firewall zone list/get`, then
use `--source-zone` and `--destination-zone` for policies.

`firewall reorder` requires one source/destination zone pair plus the **complete**
user-defined order split between `--before-system-ids` and
`--after-system-ids`. It rejects duplicates, omissions, no-ops, and system
policy injection, sends one atomic official ordering request, and verifies the
complete final order. It never falls back to per-rule updates.

`firewall move <policy> --before|--after <policy>` reads and binds the complete
order, requires both policies to be user-defined in the same zone pair, sends
one complete replacement, and verifies the final order. Custom-zone and traffic
matching-list CRUD preserve complete official documents and remain
experimental. Traffic lists support ports, IPv4 addresses, and IPv6 addresses.
Classic firewall rulesets and switching/RADIUS writes are unsupported.

### Action acceptance

`device restart/adopt/forget/locate/upgrade` and `client reconnect` return
`data: {"accepted": true}`. This does not claim that an asynchronous action
such as restart, locate, upgrade, or reconnect has completed. Adoption first
validates the returned ID and MAC; forget reports acceptance only after the
device is absent. Observable rename, block/unblock, port, resolver, and CRUD
mutations return verified observed state instead.

### Bounded detail reads

Unfiltered `port list` and `dns resolvers list` use official overview results
and at most four concurrent official detail requests. Results remain
deterministic. If any required detail request fails, the whole command fails
rather than returning partial data. `port list --device <id|mac|exact-name>`
and `port get` resolve one device and make one detail request.

## JSON and version contract

`--json` emits schema version `"1"`:

```json
{
  "schema_version": "1",
  "ok": true,
  "resource": "device",
  "action": "restart",
  "data": {"accepted": true},
  "meta": {
    "site": "default",
    "dry_run": false,
    "experimental": true
  }
}
```

Top-level `schema_version`, `ok`, `resource`, `action`, `data`, and `meta` are
always present. `data` remains present on failures (as `null`). `error` is
present only for failures and `plan` only for plan output. List responses may
include optional `meta.count`, but current typed resource-list commands omit
it. `meta.experimental` is present and true for every experimental mutation
plan, gate error, and successful apply; stable commands omit it. The v1
surface has no `--raw` flag and never embeds upstream controller payloads.
The executable JSON Schema 2020-12 contract is checked in at
[`schemas/schema-v1.json`](schemas/schema-v1.json). Stable golden output,
failures, plans, and experimental common envelopes validate against it in the
test suite.

Version interfaces are:

```bash
unifi --version
unifi version
unifi version --json
```

`version --json` uses the same envelope and returns exactly `version`,
`commit`, `build_date`, and `go_version` in `data`.

## Development and verification

```bash
./scripts/smoke.sh
go test -race ./... -timeout 30m
./scripts/check-coverage.sh
UNIFI_RELEASE_HOST=1 ./scripts/check-performance.sh
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
```

The default smoke run builds, checks formatting, vets, and runs all unit tests;
it does not contact a controller. The authenticated live suite is opt-in with
`UNIFI_IT=1` and is read-only. Non-DNS live mutations require a dedicated
sacrificial controller and are never run against a production-like network.
See [CONTRIBUTING.md](CONTRIBUTING.md) and the
[RC release checklist](docs/maintainers/release-checklist.md).

The coverage gate enforces 75% across `./internal/...` plus package floors.
The performance script records three samples everywhere and enforces the
10,000-item, 1,000-detail, and warm-help budgets only when
`UNIFI_RELEASE_HOST=1` on darwin/arm64. Native credential-store qualification
uses a unique synthetic entry and deletes it. CI runs it on macOS Keychain,
Windows Credential Manager, and Linux Secret Service.

## Security and release information

Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).
See [CHANGELOG.md](CHANGELOG.md), [Compatibility](docs/compatibility.md), and
the [`v1.0.0` release notes](docs/releases/v1.0.0.md).

## License

This project is available under the [MIT License](LICENSE).
