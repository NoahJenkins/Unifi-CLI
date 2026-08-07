# Contributing

Thanks for helping improve `unifi-cli`. The project is pre-1.0, so discuss large command or compatibility changes in an issue before investing in an implementation.

## Development setup

Install the Go version declared in `go.mod`, clone the repository, and run:

```bash
./scripts/smoke.sh
go test -race ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
```

The smoke script builds the CLI, checks `gofmt`, runs `go vet`, and executes unit tests. Pull requests run the same gate on Linux, macOS, and Windows, plus `govulncheck` on Linux.

## Pull requests

- Keep changes focused and add tests for behavior changes and bug fixes.
- Preserve the plan-first mutation model: no write may apply without `--yes`, and `--dry-run` must always win.
- Do not weaken API-key redaction, controller scoping, fallback-file permissions, or the read-only live-test allowlist.
- Update `README.md` and other current documentation when user-visible behavior changes.
- Treat files under `docs/superpowers/` as historical decision records; do not use them as current user instructions.

## Live controller testing

Live tests are optional and must target only a controller you own or are authorized to test:

```bash
export UNIFI_HOST=... UNIFI_API_KEY=... UNIFI_INSECURE=true
UNIFI_IT=1 ./scripts/smoke.sh
```

The live suite is read-only. Never commit generated reports, controller payloads, identifiers, credentials, or local configuration.

## Security reports

Do not open a public issue for vulnerabilities. Follow [SECURITY.md](SECURITY.md).
