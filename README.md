# unifi-cli

Manage a **local** UniFi Network controller (Cloud Gateway, Express APs, switches) from the terminal. Built for humans and agents: tables by default, stable JSON envelopes with `--json`, and mutations that never apply without `--yes`.

> **Local controller only.** This CLI talks to your on-network UniFi Network Application / Cloud Gateway API. It does **not** use UniFi Site Manager / cloud APIs. Local gateways often use self-signed TLS — set `insecure: true` (or `UNIFI_INSECURE=true`).

## Install

```bash
# build from source
go build -o unifi ./cmd/unifi

# or install into GOPATH/bin
go install github.com/noahjenkins/unifi-cli/cmd/unifi@latest
```

Requires Go 1.26.5.

## Configuration

Default config path: `~/.config/unifi-cli/config.yaml`  
Overrides: `--config`, `UNIFI_CONFIG`

Copy the example:

```bash
mkdir -p ~/.config/unifi-cli
cp configs/config.example.yaml ~/.config/unifi-cli/config.yaml
# edit the controller connection settings
```

Example (`configs/config.example.yaml`):

```yaml
host: 192.168.1.1
port: 443
insecure: true          # local gateway often uses self-signed TLS
site: default
safe_mode: true
timeout: 30s
```

### Environment variables

| Variable | Purpose |
|----------|---------|
| `UNIFI_HOST` | Controller host |
| `UNIFI_PORT` | Port (default 443) |
| `UNIFI_SITE` | Site name/id |
| `UNIFI_API_KEY` | Process-only API-key override for CI and scripts |
| `UNIFI_INSECURE` | Skip TLS verify (`true`/`1`) |
| `UNIFI_SAFE_MODE` | Extra guards on destructive ops |
| `UNIFI_CONFIG` | Config file path |
| `UNIFI_TIMEOUT` | Request timeout (e.g. `30s`) |

The YAML config contains controller connection settings only. To authenticate
interactively, run `unifi login`; it asks for an API key through a hidden
prompt, validates it with the controller, and saves it for later CLI runs.
`UNIFI_API_KEY` overrides that saved key for the current process, making it
appropriate for CI and scripts without writing the key locally.

```bash
unifi login
unifi auth status --json
unifi logout
unifi config show          # effective non-secret controller configuration
unifi config path          # print default config path
```

### Saved API keys

After a successful login, the CLI saves the API key in the operating system's
native credential store, so it remains available after restarting the CLI:

- macOS: Keychain
- Windows: Credential Manager
- Linux: Secret Service

Linux requires an available Secret Service provider, such as GNOME Keyring or
KWallet. On a headless Linux machine without one, explicitly opt in to the
protected local-state fallback:

```bash
unifi login --file-fallback
```

The fallback is never enabled implicitly and writes protected local API-key
state only. `unifi logout` removes the locally saved key for the configured
controller; it does not make a remote logout request. Saved keys are scoped to
one controller and are not transferable between machines. If the controller
returns `401`, run `unifi login` again with a valid API key.

## Global flags

| Flag | Description |
|------|-------------|
| `--json` | Machine-readable envelope on stdout |
| `--site` | Override configured site |
| `--dry-run` | Plan only; never apply (wins over `--yes`) |
| `--yes` | Apply mutation |
| `--force` | Override `safe_mode` blocks (still needs `--yes`) |
| `--timeout` | Per-command timeout |
| `--config` | Config path |
| `--quiet` | Suppress audit stderr |
| `--raw` | Include upstream UniFi payload under `raw` in JSON |

Identifiers resolve as: internal id → MAC (normalized) → exact name. Ambiguous matches exit with `ambiguous_id`.

## Safety model

1. **Reads** are live and unguarded.
2. **Writes** always build a change plan first. Nothing is applied unless `--yes` is set **and** `--dry-run` is not set. If both are set, **`--dry-run` wins**.
3. **`safe_mode`** (default `true`) blocks highest-impact ops unless `--force --yes`:
   - `device forget`
   - WAN / destructive network delete
4. API keys are never printed (`auth status`, `config show`, and plans mask WLAN secrets).
5. Successful applies emit an audit line on stderr (unless `--quiet`).

```bash
# plan only (default for mutations)
unifi device rename office-ap --name office-ap-2

# apply
unifi device rename office-ap --name office-ap-2 --yes

# dry-run always wins
unifi device rename office-ap --name office-ap-2 --yes --dry-run

# destructive under safe_mode
unifi device forget <id> --force --yes
```

## Command index

| Resource | Commands |
|----------|----------|
| `auth` | `status` |
| `login` | Validate and save an API key |
| `logout` | Remove the saved API key for the configured controller |
| `config` | `path`, `show` |
| `site` | `list`, `get` |
| `device` | `list`, `get`, `rename`, `restart`, `locate`, `upgrade`, `adopt`, `forget` |
| `client` | `list`, `get`, `reconnect`, `block`, `unblock` |
| `network` | `list`, `get`, `create`, `update`, `delete` |
| `wlan` | `list`, `get`, `create`, `update`, `delete`, `enable`, `disable` |
| `port` | `list`, `get`, `update` |
| `firewall` | `list`, `get`, `create`, `update`, `delete`, `reorder` |
| `dns` | `list`, `get`, `create`, `update`, `delete` |
| `dns resolvers` | `list`, `set` |
| `system` | `health`, `events`, `alerts` |

## Agent usage examples

```bash
# Inventory
unifi device list --json
unifi client list --json
unifi system health --json

# Wireless
unifi wlan list --json
unifi wlan disable Guest --yes --json

# DNS
unifi dns list --json
unifi dns create --name nas.lan --ip 192.168.1.50 --yes --json
unifi dns resolvers list --json

# Switching
unifi port list --device office-sw --json
unifi port update office-sw 12 --poe off --yes --json

# Firewall (always plan first)
unifi firewall list --json
unifi firewall delete <id>          # prints plan, no apply
unifi firewall delete <id> --yes    # applies
```

JSON success envelope shape:

```json
{
  "ok": true,
  "data": {},
  "meta": { "site": "default", "dry_run": false },
  "plan": null,
  "error": null
}
```

## Development

```bash
go build -o dist/unifi ./cmd/unifi
go vet ./...
go test ./...

# optional live smoke against a real controller
export UNIFI_HOST=... UNIFI_API_KEY=... UNIFI_INSECURE=true
UNIFI_IT=1 ./scripts/smoke.sh
```

Without `UNIFI_IT=1`, `scripts/smoke.sh` builds the binary, runs `go vet`, and runs unit tests.

With `UNIFI_IT=1`, it also runs the authenticated read-only suite. The suite
checks auth status, local configuration, every implemented list command,
system health/events/alerts, and a derived `get` for each populated resource
list. Empty firewall-rule and local-DNS lists are reported as `not_configured`
rather than failures. It never calls `login`, a mutation command, or an
apply/raw flag.

Each live run writes a redacted report to `dist/test-reports/`. Reports contain
only command names, statuses, durations, and fixed safe summaries; they do not
contain controller payloads, identifiers, arguments, credentials, or stderr.
