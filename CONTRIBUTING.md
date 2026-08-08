# Contributing

Thanks for helping improve `unifi-cli`. Discuss changes that alter command
semantics, the schema-v1 envelope, compatibility, security posture, or the
stable/experimental boundary before investing in implementation.

## Official-API-first policy

New controller behavior must use the official local UniFi Network integration
API whenever that API exposes the required operation. Do not add a cloud/Site
Manager dependency. A legacy local endpoint is acceptable only when the
official 10.3.58+ surface lacks the capability and all of the following are
true:

- the command is explicitly classified experimental;
- the plan, risk, gate, and verification behavior are documented and tested,
  including immutable-target behavior when a pre-existing target exists;
- no stable official command is silently routed through the legacy endpoint;
- the contribution explains the compatibility need and removal path.

Prefer typed DTOs and preserve complete official writable documents when the
upstream operation is full-document PUT rather than patch semantics. Fail
closed instead of inventing required controller defaults.

## Fixtures and secret hygiene

Fixtures must be derived from the applicable official OpenAPI schema or public
official examples, then reduced to synthetic data. Literal-test discriminator
unions, required fields, bounds, pagination metadata, empty collections, and
malformed/error paths.

Never record or commit live controller payloads, API keys, WLAN passwords,
hostnames, IP inventories, site/device/client identifiers, local configuration,
generated reports, or release binaries. Do not paste secrets into test failure
messages, PR descriptions, or issue comments.

## Test-driven workflow

For behavior changes:

1. Add a focused failing regression that demonstrates the missing contract.
2. Implement the smallest official-API-first change that makes it pass.
3. Run focused tests, then the repository gates below.
4. Update current documentation when command behavior, compatibility, or risk
   changes.

```bash
./scripts/smoke.sh
go test -race ./...
./scripts/check-coverage.sh
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
```

The smoke script builds the CLI, checks `gofmt`, runs `go vet`, and runs the
unit suite. Coverage must remain at or above the repository threshold for
`internal/cli`. Pull requests run the platform matrix; security-sensitive
changes must also exercise negative paths for credential leakage, redirect
handling, target drift, gate ordering, verification mismatch, and no-retry
behavior where applicable.

## Mutation invariants

- No write applies without `--yes`; `--dry-run` always wins.
- Experimental applies require `--experimental`.
- High-impact and destructive applies require `--force` while `safe_mode` is
  enabled.
- A target selected by name or MAC must be converted to an immutable ID before
  apply and revalidated against the prepared snapshot.
- Observable operations must verify controller state. Action-only operations
  report acceptance, never invented completion.
- API keys and WLAN passphrases never enter argv, plans, JSON, logs, or reports.
- The schema-v1 envelope retains always-present `data` and omits inapplicable
  `error` and `plan`; there is no raw upstream-payload escape hatch.

## Live controller gates

Unit tests and schema-derived fixtures are required; they are not permission to
mutate a live controller.

- The standard `UNIFI_IT=1 ./scripts/smoke.sh` suite is read-only.
- The approved isolated DNS A-record lifecycle may run only when its temporary
  name/IP and cleanup behavior have been reviewed for that authorized lab.
- **Every non-DNS write requires a dedicated sacrificial controller and
  disposable network configuration.** Never run it on a home, office,
  customer, shared, production, or production-like controller.
- Do not point mutation tests at a controller merely because you own it. If it
  carries real clients, routing, DNS, firewall policy, WiFi, or switch-port
  state, it is not sacrificial.
- Reconfirm controller version, authorization, backups/reset path, and cleanup
  before each live mutation gate. Stop on any unexpected state.

Live testing is an explicit release-gate activity, not part of ordinary local
development. Keep reports private, redacted, and uncommitted.

## Pull requests

- Keep changes focused and include the official API version/source used.
- Add tests for every behavior change and bug fix.
- Preserve auth redaction, controller scoping, protected fallback-file
  permissions, TLS defaults, and the read-only live-test allowlist.
- Update `README.md`, `docs/compatibility.md`, release notes, or the changelog
  when their contracts change.
- Treat `docs/superpowers/` as dated decision records. Current root and
  compatibility documentation takes precedence.

Dependabot patch and minor updates are grouped by ecosystem. Major updates and
changes that affect controller wire behavior require manual review.

## Security reports

Do not disclose vulnerabilities publicly. Follow [SECURITY.md](SECURITY.md).
