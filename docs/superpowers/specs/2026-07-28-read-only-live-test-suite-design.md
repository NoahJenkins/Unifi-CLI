# Read-Only Live Test Suite Design

**Date:** 2026-07-28  
**Status:** Approved for implementation planning  
**Scope:** Live, authenticated, read-only verification of the `unifi` CLI

## Goal

Provide a repeatable integration test that proves every currently implemented
read-only CLI capability works against the configured local UniFi controller.
The test must never invoke a mutation endpoint or change controller state.

## Test runner

Extend `scripts/smoke.sh`. Existing behavior remains unchanged unless
`UNIFI_IT=1` is set:

- Without `UNIFI_IT=1`, build the binary and run `go test ./...` only.
- With `UNIFI_IT=1`, require `UNIFI_HOST` and either `UNIFI_API_KEY` or
  `UNIFI_USERNAME` plus `UNIFI_PASSWORD`, then run the read-only live suite.
- The runner uses the built binary and passes `--json` to every live command.
- No command includes `--yes`; no mutation command is named, including a
  dry-run mutation command.

## Capability coverage

The suite verifies these commands:

| Area | Commands |
|---|---|
| Authentication | `auth status`, `auth login` |
| Local configuration | `config path`, `config show` |
| Sites | `site list`, then `site get <id>` when a site exists |
| Devices | `device list`, then `device get <id>` when a device exists |
| Clients | `client list`, then `client get <id>` when a client exists |
| Networks | `network list`, then `network get <id>` when a network exists |
| WLANs | `wlan list`, then `wlan get <id>` when a WLAN exists |
| Switch ports | `port list`, then `port get <device> <index>` when a port exists |
| Firewall | `firewall list`, then `firewall get <id>` when a rule exists |
| Local DNS | `dns list`, then `dns get <id>` when a record exists |
| DNS resolvers | `dns resolvers list` |
| System | `system health`, `system events`, `system alerts` |

`config path` and `config show` are local commands and do not require the
controller. They remain in the suite because they are read-only CLI functions
and `config show` must continue to redact secrets.

## Outcome model

Each command produces one of three outcomes:

- **pass** — exits zero and returns a valid successful JSON envelope.
- **not_configured** — an optional resource list succeeds but is empty, so its
  dependent `get` check is skipped. This is a passing outcome, reported
  explicitly rather than hidden.
- **fail** — the command exits non-zero, returns malformed JSON, has
  `ok: false`, or violates its expected contract.

The suite fails overall if any capability fails. It does not fail simply
because an optional resource such as a DNS record or firewall rule has not
been configured.

## JSON contracts

For every controller-backed command, the runner checks:

- syntactically valid JSON;
- `ok` is exactly `true`;
- a matching resource and action, accounting for nested actions such as
  `dns resolvers list`;
- data has the expected top-level shape (array for list commands, object for
  `get` and health commands);
- list counts agree with the returned array when a count is present.

For populated list results, the runner selects the first normalized ID from
the data and passes it to the associated `get` command. Ports instead select a
device identifier and port index from `port list` output.

The runner must not assert a fixed inventory size, device name, IP address, or
specific event/alert contents. Live controller state is expected to vary.

## Privacy and reporting

Console output is a concise command-by-command status summary. Raw controller
payloads, credentials, API keys, and rendered configuration values are never
printed. The runner writes a timestamped, redacted JSON report beneath
`dist/test-reports/`, which is ignored by Git. The report contains command,
status, duration, and a safe failure summary; it does not contain resource
payloads or secrets.

## Error handling

- Use `set -euo pipefail` and collect each command's exit status so the final
  report contains all attempted capabilities, rather than stopping at the
  first failure.
- Treat unavailable or version-incompatible controller endpoints as failures,
  because this suite is intended to reveal unsupported live behavior.
- Preserve the command's sanitized error code/message in the report when it
  can be extracted safely; otherwise record its exit status only.

## Non-goals

- Mutating endpoint checks, including plan/dry-run validation.
- Performance, load, or security testing of the controller.
- Assertions about the exact composition of the user's network.
- Uploading reports or telemetry to an external service.

## Verification

Implementation is complete when:

1. Unit tests cover the runner's JSON parsing, empty-resource handling, and
   failure aggregation without a live controller.
2. `go test ./...` remains green.
3. `UNIFI_IT=1 ./scripts/smoke.sh` completes against the authenticated
   controller and produces a redacted report.
4. A code inspection confirms the live command registry includes no mutation
   verb and no `--yes` argument.
