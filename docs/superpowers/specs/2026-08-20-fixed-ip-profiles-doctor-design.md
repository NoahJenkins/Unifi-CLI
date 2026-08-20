# Fixed-IP Inventory, Controller Profiles, and Doctor Design

**Date:** 2026-08-20
**Status:** Approved and implemented locally
**Scope:** Fixed-IP reservation reads and offline targets, named controller
profiles, local diagnostics, checkout reconciliation, and local v1 installation

## Goal

Make `unifi` dependable for repeated Home Lab automation. The CLI must expose
the complete fixed-IP workflow needed by an agent, retain controller context
without storing secrets in configuration, and diagnose local readiness before
a controller command starts.

## Constraints

- Stable commands continue to use only the official local integration API.
- Fixed-IP behavior remains an experimental legacy compatibility surface.
- No write applies without `--yes`; `--dry-run` always wins.
- Fixed-IP applies require `--experimental --force --yes` while `safe_mode` is
  enabled.
- A fixed-IP target is reduced to an immutable legacy user ID and MAC before
  apply, then revalidated immediately before one non-retried write.
- Observable writes are read back and verified.
- Fixed-IP operations never reconnect a client automatically.
- API keys remain only in the environment, native credential store, or the
  existing explicitly selected protected-file fallback.
- Profile files and diagnostic output never contain or reveal API keys.
- Existing flat configuration and environment-variable workflows remain
  compatible.
- Ordinary development performs no live controller writes.

## Delivery Order

1. Fetch the current remote state, preserve the divergent local `main` commit
   on a backup branch, and align local `main` with `origin/main` without losing
   the existing commit.
2. Create an isolated `codex/` feature worktree from the reconciled main branch.
3. Install the published `v1.0.0` executable at the existing Go binary path and
   verify both version interfaces before changing product code.
4. Implement fixed-IP reads and offline target support.
5. Implement named profiles.
6. Implement local diagnostics.
7. Create and select a non-secret `home-lab` profile from safely discovered
   local controller information.
8. Run focused tests and all repository gates. Run no live mutation gate.

The old Keychain item is preserved. Installation or profile setup must not
delete, rewrite, migrate, or print that item.

## Fixed-IP Command Contract

The `client fixed-ip` group contains four commands:

```text
unifi client fixed-ip list
unifi client fixed-ip get <client>
unifi client fixed-ip set <client> <ipv4>
unifi client fixed-ip clear <client>
```

### List

`client fixed-ip list` reads legacy user records and returns only records whose
`use_fixedip` value is true. It includes connected and offline clients.

Each result contains exactly the normalized reservation fields already used by
mutation output:

```text
client_id
mac
name
network_id
fixed_ip_enabled
fixed_ip
```

Human output is a deterministic table. JSON output uses the schema-v1 envelope
with `resource: "client"` and `action: "fixed-ip list"`.

### Get

`client fixed-ip get <client>` resolves one legacy user record by exact ID,
normalized MAC, or exact name. It returns enabled and disabled records so an
operator can distinguish “known client without a reservation” from “client not
found.” Ambiguous name or MAC matches fail closed.

The normalized output uses the same six reservation fields as list. For a
disabled reservation, `fixed_ip_enabled` is false and `fixed_ip` is empty even
if the controller retains an inactive historical address internally.

### Offline set and clear

Set and clear resolve the legacy user record directly instead of requiring the
client to appear in the active-station list. An active-client match remains
valid but is not required.

Set requires the resolved user record to have:

- an immutable legacy user ID;
- a valid normalized MAC;
- a network ID that resolves to exactly one network;
- DHCP enabled on that network;
- a usable IPv4 subnet.

If any field is absent or ambiguous, planning fails before a write. The CLI
does not infer a network from an old IP address, client name, VLAN, or DHCP
range.

Clear requires an enabled reservation and the same immutable ID and MAC. It
does not require the client to be online. It sends only `_id` and
`use_fixedip: false`, preserving the controller's inactive stored address.

### Validation and verification

Set keeps the current validation rules:

- IPv4 only;
- inside the resolved network subnet;
- not the network, broadcast, or gateway address;
- not an enabled reservation on another user record;
- not the current IP of another connected client.

The CLI does not claim to detect an offline device that uses an unrecorded
static address. Documentation must state this proof limit.

Set writes `_id`, `use_fixedip: true`, `network_id`, and `fixed_ip`. Clear
writes `_id` and `use_fixedip: false`. Each apply performs one PUT to the
legacy `rest/user/{id}` endpoint and one verification read. It does not retry
an ambiguous write.

## Controller Profile Design

Profiles are non-secret configuration files stored by default at:

```text
~/.config/unifi-cli/profiles/<name>.yaml
```

The selected profile name is stored separately at:

```text
~/.config/unifi-cli/current-profile
```

The marker contains one validated profile name and a trailing newline. The
profile directory is mode `0700`, and files created or rewritten by the CLI
are mode `0600`. Existing user-created files must be regular files, must not
be symlinks, and must pass the existing bounded strict YAML parser.

Profile names are 1–64 characters, start with an ASCII letter or digit, and
contain only ASCII letters, digits, `.`, `_`, or `-`. Path separators,
whitespace, `.` and `..` are rejected.

Each profile file uses the existing non-secret flat schema:

```yaml
host: controller.lan
port: 443
ca_cert: /path/to/controller-ca.pem
insecure: false
site: default
safe_mode: true
timeout: 30s
```

`ca_cert` and `insecure: true` remain mutually exclusive. Credential fields
remain rejected.

### Selection precedence

Configuration selection uses this order:

1. `--config` command flag.
2. `UNIFI_CONFIG` environment variable.
3. `--profile` command flag.
4. `UNIFI_PROFILE` environment variable.
5. The validated `current-profile` marker.
6. The existing default `~/.config/unifi-cli/config.yaml`.

Explicit config selection bypasses profiles. Supplying both an explicit config
and an explicit profile fails with a validation error instead of silently
choosing one. `--profile` overrides `UNIFI_PROFILE`. A missing selected profile
reports its resolved path and suggests `unifi config profile list`; it does not
fall back to another controller.

### Profile commands

```text
unifi config profile list
unifi config profile show [name]
unifi config profile select <name>
```

- `list` reads valid regular `*.yaml` files, sorts by name, and marks the
  selected profile. A malformed profile is reported as invalid without
  printing its contents; one invalid file does not hide other profiles.
- `show` defaults to the selected profile. It emits the normalized non-secret
  configuration, validity, path, and selection state.
- `select` fully loads and validates the target profile before atomically
  replacing the marker. It does not access the credential store or controller.

Profile creation and deletion commands are out of scope. Profiles remain
ordinary reviewable YAML files created from the existing example. This keeps
the command surface small and avoids a second configuration editor.

`config path` reports the effective selected file. `config show` reports the
effective configuration and adds the profile name when a profile supplied it.

## Doctor Command

`unifi doctor` is a local-only readiness check. It never sends a controller
request and never writes configuration or credential state.

It reports:

```text
version
commit
config_path
profile
host
site
tls_mode
credential_source
ready
```

`tls_mode` is `system_roots`, `custom_ca`, or `insecure`. `credential_source`
is `environment_api_key`, `saved_api_key`, `missing`, or
`keyring_unavailable`. The API key value is never included in output, errors,
logs, tests, or JSON.

`ready` is true only when the version metadata is available, configuration is
valid, a host is selected, TLS configuration is valid, and a credential source
is available. Missing configuration or credentials produces a schema-v1 error
and nonzero exit status with a specific next action. Network reachability and
credential validity remain the responsibility of `unifi auth status`.

## Home Lab Profile Setup

Setup first discovers the active LAN default gateway with a read-only routing
query. The address is accepted as the controller host only when it matches
existing local UniFi evidence or a read-only HTTPS/controller check. The CLI
must not assume every default gateway is a UniFi controller.

The resulting `home-lab.yaml` contains only controller connection settings.
The profile is selected through `config profile select home-lab`. If no valid
v1 API key is available, setup stops with the profile configured and reports
that `unifi login` requires user input. It does not transform the old Keychain
value automatically.

## Error Handling

- Missing config: identify the attempted path or profile and give one safe
  corrective command.
- Invalid profile name or traversal: `validation_failed` before file access.
- Malformed or unsafe profile file: `validation_failed`; never print file
  contents.
- Missing or ambiguous legacy client: `not_found` or `ambiguous_id` with no
  write.
- Offline client without a network ID: `conflict`; do not infer a network.
- Keychain isolation or backend failure: distinguish `keyring_unavailable`
  from a missing key. Do not claim credentials are invalid.
- Controller validation failures remain separate from local doctor results.

## Compatibility and Documentation

Update `README.md` for the four fixed-IP commands, offline-client rules,
profile precedence, Home Lab-style setup, and doctor output. Update
`docs/compatibility.md` to state that fixed-IP inventory and offline mutations
use the experimental legacy user endpoint and retain the documented static-IP
proof limit. Update `CHANGELOG.md` because the public command surface changes.

The schema-v1 envelope remains unchanged. New action names are
`fixed-ip list`, `fixed-ip get`, `profile list`, `profile show`,
`profile select`, and `doctor`.

## Testing

Development follows red-green-refactor cycles.

Focused tests cover:

- enabled-only fixed-IP list output and deterministic ordering;
- get for enabled, disabled, missing, and ambiguous user records;
- offline set and clear planning, payloads, drift, and verification;
- missing offline network identity and all current address conflicts;
- no reconnect request and no ambiguous-write retry;
- profile-name validation and traversal rejection;
- selection precedence and explicit config/profile conflicts;
- unsafe files, malformed YAML, missing selected profiles, and atomic marker
  replacement;
- deterministic profile list/show/select human and JSON output;
- doctor success, each readiness failure, keyring isolation, and complete
  credential redaction;
- legacy flat-config compatibility.

Before completion, run:

```bash
./scripts/smoke.sh
go test -race ./...
./scripts/check-coverage.sh
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
```

The standard live suite is not required for local implementation. If valid
Home Lab credentials become available, only the guarded read-only suite can be
considered, and its result is reported separately. No fixed-IP live write is
authorized by this design.

## Out of Scope

- Creating never-seen MAC records.
- Automatically choosing or changing a client's network.
- DHCP lease renewal or automatic reconnect.
- Detecting unrecorded offline static-IP use.
- Profile creation, deletion, synchronization, or cloud storage.
- API-key migration from unknown pre-v1 Keychain formats.
- Automatic release checks, self-update, controller discovery, or cloud/Site
  Manager access.
- Commit, push, pull request, release, deployment, or live controller mutation.
