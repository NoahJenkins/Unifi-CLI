# Contributing

Thanks for helping improve `unifi-cli`. The project is pre-1.0, so discuss large command or compatibility changes in an issue before investing in an implementation.

## Development setup

Install the Go version declared in `go.mod`, clone the repository, and run:

```bash
./scripts/smoke.sh
go test -race ./...
./scripts/check-coverage.sh
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
```

The smoke script builds the CLI, checks `gofmt`, runs `go vet`, and executes unit
tests. The coverage script requires at least 50% statement coverage in
`internal/cli`. Pull requests run smoke on Linux, macOS, and Windows, enforce
coverage in the Ubuntu matrix job, and run `govulncheck` on Linux.

## Pull requests

- Keep changes focused and add tests for behavior changes and bug fixes.
- Preserve the plan-first mutation model: no write may apply without `--yes`, and `--dry-run` must always win.
- Do not weaken API-key redaction, controller scoping, fallback-file permissions, or the read-only live-test allowlist.
- Never accept API keys or WLAN passwords as command arguments. Preserve hidden prompts, bounded stdin input, and redacted plans and reports.
- Update `README.md` and other current documentation when user-visible behavior changes.
- Treat files under `docs/superpowers/` as historical decision records; do not use them as current user instructions.

Dependabot patch and minor version updates are grouped by ecosystem. Patch and
minor Dependabot PRs are configured for squash auto-merge, including separate
security-update PRs; GitHub merges them only after all `main` ruleset checks
pass. Major updates remain open for manual review.

## Live controller testing

Live tests are optional and must target only a controller you own or are authorized to test:

```bash
export UNIFI_HOST=... UNIFI_API_KEY=... UNIFI_INSECURE=true
UNIFI_IT=1 ./scripts/smoke.sh
```

The live suite is read-only. Reports contain only fixed summaries and redacted
numeric exit codes. Never commit generated reports, controller payloads,
identifiers, credentials, or local configuration.

## Security reports

Do not open a public issue for vulnerabilities. Follow [SECURITY.md](SECURITY.md).
