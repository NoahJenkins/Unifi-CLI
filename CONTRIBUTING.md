# Contributing

Thanks for helping improve `unifi-cli`. Discuss changes that alter command
semantics, the schema-v1 envelope, compatibility, security posture, or the
stable/experimental boundary before investing in implementation.

## Official-API-first policy

New controller behavior must use the official local UniFi Network integration
API whenever that API exposes the required operation. Do not add a cloud/Site
Manager dependency. A legacy local endpoint is acceptable only when the
official 10.4.57+ surface lacks the capability and all of the following are
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

## Automated tool-gap reports

Automated tool-gap reports are public. Do not include credentials, controller
responses, hostnames, addresses, identifiers, network inventory, local paths,
screenshots, or raw logs. Report security defects only through the private
process in [SECURITY.md](SECURITY.md).

## Test-driven workflow

For behavior changes:

1. Add a focused failing regression that demonstrates the missing contract.
2. Implement the smallest official-API-first change that makes it pass.
3. Run focused tests, then the repository gates below.
4. Update current documentation when command behavior, compatibility, or risk
   changes.

```bash
./scripts/smoke.sh
go test -race ./... -timeout 30m
./scripts/check-coverage.sh
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
```

The smoke script builds the CLI, checks `gofmt`, runs `go vet`, and runs the
unit suite. Coverage must remain at or above the repository threshold for
`internal/cli`. Pull requests run the platform matrix; security-sensitive
changes must also exercise negative paths for credential leakage, redirect
handling, target drift, gate ordering, verification mismatch, and no-retry
behavior where applicable.

## API contract emulator

Changes to request builders, emulator behavior, contract rules, version profiles,
or synthetic seeds require the explicit cached contract check for both pinned
versions, in addition to the gates above. See the [coverage manifest and
commands](internal/testutil/contractapi/README.md). Tests stay offline; the
separate fetch script downloads only public, digest-pinned schemas into a
gitignored cache. Never commit or upload those files. Record the tested commit,
command, digests, and result without payloads. A missing cache is an incomplete
check, not a pass. Cached results use the documented, reviewed correction for
contradictory named-protocol enums and do not establish live qualification.

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
- Approved isolated DNS lifecycles may run only when every temporary value,
  disabled state, captured-ID cleanup rule, and final baseline comparison has
  been reviewed for that authorized lab.
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

Dependabot patch and minor updates are grouped by ecosystem. Go module
security updates are grouped separately. Major updates and changes that affect
controller wire behavior require manual review. Dependabot patch, minor, and
GitHub Actions updates enable squash auto-merge and wait for the required
checks. A semver-major update auto-merges only when fetch-metadata reports an
associated advisory (`alert-state` OPEN or FIXED, or a `ghsa-id` that is not
DISMISSED). That lookup runs only when the Dependabot secret
`DEPENDABOT_METADATA_TOKEN` (Settings → Secrets and variables → Dependabot,
not an Actions secret) is a personal access token or GitHub App installation
token that can read Dependabot alerts. Dependabot-triggered `pull_request`
workflows cannot read Actions secrets. A GHSA mention in release notes is not
enough. Without the secret, security majors stay for review. The auto-merge
workflow does not approve pull requests.

Release workflows pin every action to a full 40-character commit SHA.
`release_pipeline_test.go` checks that pin shape and which release actions
are present. It does not require one specific SHA, so an Actions update can
pass without editing the test. Keep the release job permissions and the other
release-contract assertions.

Dependabot's gomod updates do not bump the `go` directive. A weekly workflow
opens a pull request for the latest patch of the Go minor version already
declared in `go.mod`, then enables auto-merge for that pull request. A new
Go minor or major still needs a manual change. Opening that pull request with
the default `GITHUB_TOKEN` requires the repository workflow permission
**Allow GitHub Actions to create and approve pull requests** (Settings →
Actions → General → Workflow permissions). Those pull requests still leave CI
approval-required until a maintainer approves the workflow runs. Set the
Actions secret `GO_TOOLCHAIN_PR_TOKEN` to a GitHub App installation token or
a fine-grained personal access token with Contents and Pull requests write
so CI starts without that approval. If the permission and the secret are both
absent, the workflow reports the requirement and exits successfully. The
retired Dependabot recovery controller was removed. See
[Dependency updates](docs/maintainers/dependabot-recovery.md) for the current
path and the leftover GitHub configuration.

## CI runner preparation

All CI, release, and dependency auto-merge workflows run on GitHub-hosted
runners. The repository has no self-hosted runner pool.

CI caches Go modules and build outputs through the existing pinned setup-go
configuration and go.sum key. Go setup stops after 3 minutes. CI Go jobs
and the six release jobs that use setup-go resolve project modules in a
separate 5-minute step. The write-capable publisher keeps its single-shell
security boundary and runs a 5-minute module-download command before its Go
verification commands, without GH_TOKEN. The publisher still has its existing
10-minute job limit. Linux Secret Service installation also stops after
5 minutes. A cache miss remains supported. Test, race, coverage, fuzz, native
keyring and release smoke coverage remain unchanged, including the 40-minute
platform test ceiling.
Do not retry timed-out setup automatically or increase its deadline for a green
result. Validate Actions locally in a full clone before one hosted batch.

## Security reports

Do not disclose vulnerabilities publicly. Follow [SECURITY.md](SECURITY.md).
