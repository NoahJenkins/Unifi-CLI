# UniFi CLI Repository Instructions

These instructions apply across this repository. Follow the global agent
instructions first, then use this file for UniFi-CLI-specific work.

## Project

`unifi-cli` is an unofficial Go CLI for direct, local access to a UniFi
Network controller. The stable surface uses the official local Network
integration API. Mutations are planned before apply and fail closed when the
CLI cannot verify the requested operation safely.

Use the Go version declared in `go.mod`. Do not infer support from old plans or
release notes; verify current behavior in code, tests, `README.md`, and
`docs/compatibility.md`.

## Sources of Truth

- Read `CONTRIBUTING.md` before changing behavior. It is the detailed source of
  truth for engineering policy, mutation invariants, live testing, fixtures,
  pull requests, and secret hygiene.
- Use `README.md` for the public command, JSON, risk, and support contracts.
- Use `docs/compatibility.md` for supported controller versions and known
  limits.
- Treat `docs/superpowers/` as dated design and implementation history. Current
  code, tests, and root documentation take precedence.
- Follow `SECURITY.md` for vulnerability reports and disclosure.

## Where Work Lives

- `cmd/unifi/`: production CLI entrypoint.
- `internal/cli/`: Cobra commands, flags, prompts, output, and apply gates.
- `internal/domain/`: resource behavior, plans, validation, and verification.
- `internal/client/`: authenticated HTTP transport and official API access.
- `internal/livetest/`: guarded, read-only controller validation.
- `internal/authstore/` and `internal/privatefile/`: credential and protected
  local-file handling.
- `cmd/release-smoke/`, `scripts/`, and root release tests: release validation.

Keep package boundaries consistent. CLI code handles command interaction;
domain code owns resource rules; client code owns transport and wire formats.

## Decision Boundaries

Discuss the design and obtain approval before implementing a change that
materially affects:

- command semantics, flags, or the schema-v1 JSON envelope;
- the stable versus experimental boundary or compatibility claims;
- official versus legacy API routing or controller wire behavior;
- authentication, TLS, credential storage, redaction, or mutation safety;
- external dependencies, release behavior, or supported platforms.

Make small, reversible implementation decisions within an approved design.
Keep unrelated findings out of scope and report them separately.

## Implementation Rules

- Use the official local integration API whenever it exposes the operation.
- Fail closed instead of inventing controller defaults or claiming unverified
  completion.
- Preserve complete official writable documents for full-document PUT
  operations.
- Keep stable commands off legacy endpoints. A legacy capability must remain
  explicitly experimental and include tested gates and a removal path.
- Preserve immutable-target resolution and immediate pre-apply drift checks.
- Do not retry ambiguous writes. Verify observable mutations from controller
  state; report asynchronous actions as accepted, not completed.
- Never put API keys or WLAN passphrases in arguments, YAML, plans, JSON, logs,
  reports, fixtures, errors, issues, or pull-request text.
- Build fixtures from official schemas or public examples, then reduce them to
  synthetic data. Never commit live controller payloads or identifiers.

## Development Loop

For behavior changes:

1. Add a focused failing regression that demonstrates the required contract.
2. Confirm that it fails for the expected reason.
3. Implement the smallest official-API-first change that makes it pass.
4. Run focused tests, then the repository gates.
5. Update current documentation when behavior, compatibility, risk, or output
   contracts change.

Run these gates before reporting a repository change complete:

```bash
./scripts/smoke.sh
go test -race ./...
./scripts/check-coverage.sh
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
```

Do not weaken, skip, or disable a test to obtain a green result. If a test is
incorrect, explain the evidence before changing its contract.

## Live Controller Safety

- Unit tests and schema-derived fixtures do not authorize live mutations.
- `UNIFI_IT=1 ./scripts/smoke.sh` is read-only.
- Run the isolated DNS A-record lifecycle only for an explicitly authorized
  lab after reviewing its temporary values and cleanup behavior.
- Every non-DNS live write requires a dedicated sacrificial controller with
  disposable configuration. Never use a home, office, customer, shared,
  production, or production-like controller.
- Before an authorized live mutation gate, reconfirm the controller version,
  authorization, backup or reset path, and cleanup. Stop on unexpected state.
- Keep live reports private, redacted, protected, and uncommitted.

## Documentation and Delivery

- Update `README.md` when public commands, flags, JSON, risk, or support change.
- Update `docs/compatibility.md` when controller coverage or limits change.
- Update release notes or `CHANGELOG.md` when the release contract changes.
- Keep changes focused and preserve unrelated user work.
- Do not commit, push, publish, release, or run live controller writes unless
  the user explicitly requests that boundary.
