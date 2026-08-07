# unifi-cli

> **Unofficial project.** unifi-cli is an independent community tool and is not affiliated with, endorsed by, or sponsored by Ubiquiti Inc. UniFi is a trademark of Ubiquiti Inc.

Neither the author nor users of this project own the UniFi brand; Ubiquiti Inc. owns it.

Manage a **local** UniFi Network controller from the terminal. The
`v1.0.0-rc.1` release candidate uses the official local Network integration API
for its stable surface, emits a versioned JSON contract for automation, and
plans every mutation before it can apply.

> **Local controller only.** This CLI talks directly to the on-network UniFi
> Network Application or Cloud Gateway. It does not use UniFi Site Manager,
> UniFi cloud APIs, or the remote Connector examples shown in the upstream API
> reference.

The compatibility target is UniFi Network **10.3.58 and newer**. The current
implementation is schema- and fixture-validated against the official 10.3.58
API. Fresh live proof for this RC, including 10.4.57, is a release gate and has
not yet been completed. See [Compatibility](docs/compatibility.md) for the
exact status.

## Install `v1.0.0-rc.1`

After the RC tag is published:

```bash
go install github.com/noahjenkins/unifi-cli/cmd/unifi@v1.0.0-rc.1
unifi --version
unifi version --json
```

Release archives will also be attached to the GitHub release during the Task 9
delivery workflow. Until then, build the reviewed source checkout with the Go
version declared in `go.mod`:

```bash
go build -o dist/unifi ./cmd/unifi
./dist/unifi --help
./dist/unifi version
```

Do not treat a locally built `dev` version as the published RC. Release builds
populate the version, commit, and build date through linker metadata.

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
| Inventory and health reads | Stable official | `site`, `device`, `client`, `network`, `wlan`, `port`, `firewall`, `firewall zone`, `dns`, `dns resolvers`, and `system health` list/get operations as exposed by help |
| Local DNS A-record writes | Stable official | `dns create`, `dns update`, `dns delete` |
| Network and WiFi writes | Experimental official | Network CRUD; WLAN CRUD, enable, and disable |
| Device lifecycle writes | Experimental official | `device restart`, `device adopt`, `device forget` |
| Firewall policy writes | Experimental official | Policy create, update, delete, and atomic reorder |
| Legacy device/client/port/resolver writes | Experimental legacy | Device rename/locate/upgrade; client reconnect/block/unblock; port update; resolver set |

The legacy-experimental rows are deliberately isolated compatibility paths;
they are not described as stable official API support. There are no stable
non-DNS mutations in this RC.

## Mutation safety

Every mutation first resolves its target, validates input, and emits a plan.
It applies only when `--yes` is present. `--dry-run` always wins over `--yes`.
Experimental plans do not need opt-in, but experimental applies require both
`--experimental` and `--yes`. With `safe_mode: true`, high-impact and
destructive applies also require `--force`. `--force` never implies `--yes` or
`--experimental`.

Targeted operations bind the plan to an immutable ID and observed snapshot,
revalidate immediately before one apply attempt, and fail on drift. Verified
operations re-read controller state afterward; they do not retry an ambiguous
write.

### Exact risk and support classification

| Risk | Support/API | Commands | Apply flags with default `safe_mode` |
|---|---|---|---|
| `routine` | Stable official | `dns create`, `dns update` | `--yes` |
| `routine` | Experimental official | `device adopt`; `network create`; `wlan create/update/enable/disable` | `--experimental --yes` |
| `routine` | Experimental legacy | `device rename/locate`; `client reconnect/block/unblock` | `--experimental --yes` |
| `high_impact` | Experimental official | `device restart`; `network update`; `firewall create/update/reorder` | `--experimental --force --yes` |
| `high_impact` | Experimental legacy | `device upgrade`; `port update`; `dns resolvers set` | `--experimental --force --yes` |
| `destructive` | Stable official | `dns delete` | `--force --yes` |
| `destructive` | Experimental official | `device forget`; `network delete`; `wlan delete`; `firewall delete` | `--experimental --force --yes` |

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
policies. Stable writes are intentionally limited to **A records only** and
validate the DNS name, IPv4 address, and positive TTL locally. Updating or
deleting any other policy type fails before a write. Create/update re-read by
ID; delete verifies both ID absence and exact-domain absence.

### Networks

Network writes use the official `--management gateway|switch|unmanaged`
discriminator. The removed legacy `--purpose` vocabulary (`corporate`,
`guest`, `wan`) is not accepted for official writes.

Creation fails closed when the CLI cannot construct the required official
document:

- every create needs an explicit VLAN ID in `1..4009`;
- `unmanaged` rejects subnet and DHCP/domain fields;
- `switch` creation is rejected because the command does not expose the
  required device ID;
- `gateway` requires an IPv4 subnet with prefix `8..30` and rejects DHCP/domain
  creation because the CLI cannot safely infer the required range, lease, and
  conflict-detection fields;
- updates cannot transition between management variants because the CLI cannot
  construct every required target-mode field.

### WiFi

Official create supports OPEN and WPA2 Personal (`wpapsk`/`wpa2_personal`)
STANDARD broadcasts. Personal passphrases must be 8–63 characters and enter
through the hidden `--password` prompt or bounded `--password-stdin`, never as
an argument value. WPA3 Personal and mixed WPA2/WPA3 creation are rejected
because required SAE/PMF/fast-roaming inputs are not exposed. Enterprise modes
are rejected because RADIUS inputs are not exposed. Existing complete WPA3 or
mixed documents can be preserved during unrelated full-document updates.

### Firewall

Firewall reads and writes use modern official zones and policies; classic
rulesets are not supported. Resolve zones with `firewall zone list/get`, then
use `--source-zone` and `--destination-zone` for policies.

`firewall reorder` requires one source/destination zone pair plus the **complete**
user-defined order split between `--before-system-ids` and
`--after-system-ids`. It rejects duplicates, omissions, no-ops, and system
policy injection, sends one atomic official ordering request, and verifies the
complete final order. It never falls back to per-rule updates.

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
  "action": "list",
  "data": [],
  "meta": {
    "site": "default",
    "count": 0,
    "dry_run": false
  }
}
```

Top-level `schema_version`, `ok`, `resource`, `action`, `data`, and `meta` are
always present. `data` remains present on failures (as `null`). `error` is
present only for failures and `plan` only for plan output. List responses may
include `meta.count`. The v1 surface has no `--raw` flag and never embeds
upstream controller payloads.

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
go test -race ./...
./scripts/check-coverage.sh
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
```

The default smoke run builds, checks formatting, vets, and runs all unit tests;
it does not contact a controller. The authenticated live suite is opt-in with
`UNIFI_IT=1` and is read-only. Non-DNS live mutations require a dedicated
sacrificial controller and are never run against a production-like network.
See [CONTRIBUTING.md](CONTRIBUTING.md) and the
[RC release checklist](docs/maintainers/release-checklist.md).

## Security and release information

Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).
See [CHANGELOG.md](CHANGELOG.md), [Compatibility](docs/compatibility.md), and
the [`v1.0.0-rc.1` release notes](docs/releases/v1.0.0-rc.1.md).

## License

This project is available under the [MIT License](LICENSE).
