## Summary

<!-- What changed and why? -->

## Verification

<!-- List the commands or checks you ran. -->

- [ ] `./scripts/smoke.sh`
- [ ] `go test -race ./...` when concurrency behavior changed
- [ ] `./scripts/check-coverage.sh` when CLI commands changed
- [ ] Documentation updated for user-visible behavior

## Safety

- [ ] No credentials, controller payloads, network inventory, or generated live-test reports are included
- [ ] Mutation changes preserve `--yes`, `--dry-run`, and `safe_mode` behavior
- [ ] Credential changes preserve redaction, bounded input, and protected storage

<!-- Remove checklist items that are genuinely not applicable and explain why. -->
