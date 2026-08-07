# Security Policy

## Supported versions

This project is pre-1.0 and currently supports only the latest commit on `main`. There are no tagged releases yet.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability or include API keys, controller responses, network inventory, or other sensitive data in a report.

Use [GitHub's private vulnerability reporting form](https://github.com/NoahJenkins/Unifi-CLI/security/advisories/new). If the form is unavailable, open an issue containing only a request for private contact—do not include vulnerability details. Include the affected commit, platform, reproduction steps, impact, and the smallest redacted evidence needed to validate the report once a private channel is established. You can expect an initial acknowledgement within seven days.

Only test against systems and networks you own or are explicitly authorized to assess. A report does not authorize testing third-party UniFi controllers or infrastructure.

## Security model

- API keys belong in the native OS credential store or the explicitly selected protected-file fallback, never in repository files or command arguments.
- Mutations require `--yes`; `--dry-run` wins over `--yes`; destructive operations protected by `safe_mode` also require `--force`.
- TLS verification is the secure default. `insecure: true` is an explicit trusted-LAN compatibility fallback and permits controller impersonation by an active network attacker.
- Live integration tests must remain read-only and their reports must remain redacted.

Security fixes may be released without advance public disclosure when disclosure would put users at risk.
