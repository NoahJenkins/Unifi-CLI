# UniFi CLI Design

> **Historical record.** The core CLI was implemented, but the authentication sections were superseded by the 2026-07-29 API-key-only design. Use the root `README.md` for current behavior.

**Date:** 2026-07-27  
**Status:** Approved for implementation planning  
**Binary name:** `unifi`

## Problem

Manage a UniFi network (Cloud Gateway, UniFi Express APs, switches) from the terminal so agents and humans can inspect and change network configuration without the UniFi UI. The CLI must be safe enough for autonomous agents and expressive enough for full network administration.

## Goals

- Local UniFi Network Application (controller) API only — talk to the Cloud Gateway on the LAN/VPN.
- Full network admin surface for devices, clients, networks/VLANs, WLANs, switch ports, firewall, and DNS.
- Agent-friendly: stable JSON with `--json`, clear exit codes, deterministic IDs, dry-run plans.
- Human-friendly defaults: tables unless `--json`; mutations preview unless explicitly applied with `--yes`.
- Single static Go binary; config file + env credentials.

## Non-goals (v1)

- UniFi cloud / Site Manager API
- Interactive TUI
- High-level intent playbooks (`guest-wifi off` as a named recipe)
- Declarative bulk YAML apply / drift reconciliation
- Multi-controller fleets (single host config; site flag only)
- DPI/traffic analytics product surface (beyond basic health/events)

## Target environment

Typical home/lab site:

| Role | Hardware |
|------|----------|
| Controller + gateway | UniFi Cloud Gateway |
| APs | 3× UniFi Express (AP mode) |
| Switching | 2× UniFi switches |

One configured controller host; default site from config (overridable with `--site`).

## Approach

**Domain resource CLI** over a thin UniFi HTTP client.

Not a 1:1 raw API mirror. Commands expose stable network nouns/verbs; the client maps to controller endpoints and normalizes responses into DTOs agents can rely on.

## Architecture

```
args/flags
    ↓
CLI (Cobra) — parse, global flags, invoke domain
    ↓
Domain services — device, client, network, wlan, port, firewall, dns, site, system
    ↓
Plan layer — build before/after change plans for mutations
    ↓
UniFi client — auth, site-scoped HTTP, retries, typed errors
    ↓
Local controller (Cloud Gateway Network Application)
    ↓
Renderer — table (default) or JSON (--json)
```

### Packages

```
cmd/unifi/              # main
internal/cli/           # Cobra commands and flag wiring
internal/config/        # file + env merge
internal/client/        # UniFi HTTP session and endpoints
internal/domain/        # one package or file group per resource
internal/plan/          # dry-run change plans
internal/render/        # table + JSON envelope
```

### Dependency principles

- Go standard library HTTP client preferred.
- Cobra for commands; light YAML config (stdlib or minimal dependency).
- No mandatory third-party UniFi SDK; wrap controller REST directly so DTOs stay under our control.
- Secrets never logged or printed by `auth status` / errors.

## Configuration and auth

### Config file

Default path: `~/.config/unifi-cli/config.yaml`  
Overrides: `--config`, `UNIFI_CONFIG`

```yaml
host: 192.168.1.1
port: 443
insecure: true          # local gateway often uses self-signed TLS
site: default
username: admin         # optional if api_key set
# password: set via env preferred
# api_key: set via env preferred
safe_mode: true
timeout: 30s
```

### Environment variables

| Variable | Purpose |
|----------|---------|
| `UNIFI_HOST` | Controller host |
| `UNIFI_PORT` | Port (default 443) |
| `UNIFI_SITE` | Site name/id |
| `UNIFI_USERNAME` | Local user |
| `UNIFI_PASSWORD` | Local password |
| `UNIFI_API_KEY` | API key (preferred when present) |
| `UNIFI_INSECURE` | Skip TLS verify (`true`/`false`) |
| `UNIFI_SAFE_MODE` | Extra guards on destructive ops |
| `UNIFI_CONFIG` | Config file path |
| `UNIFI_TIMEOUT` | Request timeout |

### Auth behavior

1. If `api_key` is set, use API key auth.
2. Else use local username/password session (login cookie/CSRF as required by the controller version).
3. `unifi auth status` validates credentials and prints host/site/auth method with secrets redacted.
4. `unifi auth login` performs an explicit connectivity/auth check and caches nothing beyond normal HTTP session lifetime in-process (no long-lived plaintext password files written by the CLI).

## Command surface

Pattern: `unifi <resource> <verb> [identifier] [flags]`

### Global flags

| Flag | Description |
|------|-------------|
| `--json` | Machine-readable envelope on stdout |
| `--site` | Override configured site |
| `--dry-run` | Explicit plan-only mode. Equivalent to omitting `--yes` on mutations. If both `--dry-run` and `--yes` are set, `--dry-run` wins (nothing applied). |
| `--yes` | Apply mutation (required for any write to take effect) |
| `--force` | Override `safe_mode` blocks (still requires `--yes` for writes) |
| `--timeout` | Per-command timeout |
| `--config` | Config path |
| `--quiet` | Suppress non-essential stderr (e.g. audit line) |
| `--raw` | Include upstream UniFi payload under `raw` in JSON (escape hatch) |

### Resources and verbs

| Resource | Verbs | Scope |
|----------|-------|--------|
| `auth` | `status`, `login` | Credential validation |
| `config` | `path`, `show` | Show effective config (redacted) |
| `site` | `list`, `get` | Sites on controller |
| `device` | `list`, `get`, `rename`, `restart`, `locate`, `upgrade`, `adopt`, `forget` | Gateway, Express APs, switches |
| `client` | `list`, `get`, `reconnect`, `block`, `unblock` | Wired/wireless clients |
| `network` | `list`, `get`, `create`, `update`, `delete` | LAN/VLAN/WAN definitions |
| `wlan` | `list`, `get`, `create`, `update`, `delete`, `enable`, `disable` | SSIDs and wireless settings |
| `port` | `list`, `get`, `update` | Switch/gateway ports: profile, PoE, VLAN membership, enable/disable |
| `firewall` | `list`, `get`, `create`, `update`, `delete`, `reorder` | Rules and related groups as exposed by controller |
| `dns` | `list`, `get`, `create`, `update`, `delete` | Local DNS records (name → IP) |
| `dns resolvers` | `list`, `set` | Per-network and WAN/upstream DNS resolvers |
| `system` | `health`, `events`, `alerts` | Controller/network health and recent signals |

### Identifier resolution

Commands that take an identifier accept, in order of match:

1. UniFi internal id (`_id`)
2. MAC address (normalized, case-insensitive)
3. Exact name

If multiple resources match, exit with `ambiguous_id` and list candidates in the error payload. No silent partial match.

### Mutation policy

| Class | Examples | Requirements |
|-------|----------|--------------|
| Read | `list`, `get`, `health` | None |
| Write | `rename`, `update`, `enable`, `create` | Show plan; apply only with `--yes` |
| Destructive | `delete`, `device forget`, WAN network delete | `--yes` required; if `safe_mode`, also `--force` for highest-impact ops |

**Rule:** No mutation ever applies unless `--yes` is set and `--dry-run` is not set.

Without apply (no `--yes`, or `--dry-run` present):

1. Resolve target(s)
2. Build a change plan (`before` / `after` / ops)
3. Print plan (table or JSON)
4. Exit 0 with `meta.dry_run: true` (nothing applied)

With apply (`--yes`, and not `--dry-run`):

1. Same plan
2. Apply
3. Print result (`meta.dry_run: false`)
4. Emit one audit line on stderr unless `--quiet`

## Data contracts

### Success envelope (`--json`)

```json
{
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

### Error envelope

```json
{
  "ok": false,
  "resource": "wlan",
  "action": "get",
  "error": {
    "code": "not_found",
    "message": "WLAN not found: Guest",
    "hint": "Run unifi wlan list"
  },
  "meta": {
    "site": "default",
    "dry_run": false
  }
}
```

Non-zero exit code on `ok: false`.

### Dry-run plan envelope

```json
{
  "ok": true,
  "resource": "wlan",
  "action": "disable",
  "data": null,
  "meta": {
    "site": "default",
    "dry_run": true
  },
  "plan": {
    "summary": "Disable WLAN Guest",
    "changes": [
      {
        "op": "update",
        "resource": "wlan",
        "id": "abc123",
        "name": "Guest",
        "before": { "enabled": true },
        "after": { "enabled": false }
      }
    ]
  }
}
```

### DTO normalization

- Stable snake_case field names for agent-facing objects.
- Common device fields: `id`, `mac`, `name`, `model`, `type`, `state`, `ip`, `version`, `uplink`, `adopted`.
- Common client fields: `id`, `mac`, `hostname`, `name`, `ip`, `essid`, `network`, `is_wired`, `blocked`, `last_seen`.
- Do not require agents to know UniFi internal names (`_id`, `user_id`, etc.) unless using `--raw`.
- `--raw` adds original controller payload under `raw` without replacing normalized `data`.

### Error codes

| Code | When |
|------|------|
| `auth_failed` | Bad credentials or expired session |
| `controller_unreachable` | Dial/TLS/timeout to host |
| `not_found` | Identifier resolved to nothing |
| `ambiguous_id` | Identifier matched multiple objects |
| `conflict` | Controller rejected state conflict |
| `permission_denied` | Authenticated but not allowed |
| `validation_failed` | Bad flags/payload before call |
| `safe_mode_blocked` | Destructive op blocked without `--force` |
| `not_implemented` | Known UniFi gap / version mismatch |
| `internal` | Unexpected CLI bug |

## UniFi client responsibilities

- Base URL from host/port; TLS insecure mode when configured.
- Site-scoped paths for network application endpoints.
- Centralize endpoint mapping so domain code does not embed raw paths ad hoc without review.
- Retries only for idempotent GETs on transient network failures (bounded).
- Map HTTP status and UniFi error bodies to typed errors → error codes above.
- Version tolerance: prefer documented/stable endpoints; when fields differ across controller versions, normalize defensively and document gaps as `not_implemented` rather than crashing.

Exact endpoint paths are an implementation detail to be fixed against the running Cloud Gateway firmware during implementation, with fixtures captured from that controller.

## Output and UX

- Default: human-readable tables (and simple key/value for `get`).
- `--json`: envelope only on stdout; logs/audit on stderr.
- Exit codes: `0` success (including dry-run plans), `1` general/controller errors, `2` usage/validation errors.
- Filtering: start with server-side or client-side flags where cheap (`--name`, `--mac`, `--network`, `--type`); avoid a full query language in v1.

## Safety model summary

1. Reads are live and unguarded.
2. Writes preview a plan; `--yes` applies.
3. `safe_mode` (default true) blocks `device forget` and WAN/network deletes unless `--force --yes`.
4. No command prints passwords or API keys.
5. Audit line on apply to stderr for operator trails.

## Testing strategy

| Layer | What |
|-------|------|
| Unit | Identifier resolution, config merge, plan diff, error mapping, renderers |
| Client | HTTP fixtures (recorded JSON) for list/get/update per resource |
| Domain | Service tests with fake client interface |
| Smoke | Optional live tests behind `UNIFI_IT=1` against a real controller |

Golden files for JSON envelopes to lock agent contracts.

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
unifi firewall delete <id> --yes    # applies (may need --force if safe_mode)
```

## Implementation phases (planning hint)

Not a full implementation plan — guidance for the next planning step:

1. Scaffold Go module, config, client auth, `auth status`, render envelope.
2. Read path: `device`, `client`, `system health`, `site`.
3. Wireless + networks read/write with dry-run plans.
4. Ports + DNS.
5. Firewall + destructive guards + safe_mode.
6. Adopt/forget/upgrade and polish (docs, shells completions optional).

## Decisions log

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Control plane | Local controller API | Fullest admin for Cloud Gateway site |
| Shape | Domain resource CLI | Stable agent contracts vs raw API mirror |
| Language | Go | Single binary, solid CLI ergonomics |
| Output | Tables default, `--json` opt-in | Humans and agents both first-class |
| Auth | API key or user in config/env | Standard ops pattern |
| Mutations | Plan + `--yes` | Safe default for agent drivers |
| DNS | First-class `dns` resource | Explicit agent surface for local records and resolvers |
| Playbooks / cloud API | Deferred | YAGNI until resources are solid |

## Open implementation details

These do not block the design; resolve during implementation against the live controller:

- Exact auth header/cookie scheme for the installed Network Application version
- Precise firewall object model (rules vs groups vs zones) as exposed by firmware
- Whether Express units appear strictly as `type=uap` or mixed gateway/AP roles
- PoE and port profile enumerations per switch model
