# Security Policy

## Supported versions

| Version | Security support |
|---|---|
| `v1.0.0-rc.1` | Supported until it is superseded by a newer RC or stable 1.x release |
| Latest stable 1.x | Supported once published |
| Older RCs, older 1.x minors, and pre-1.0 development commits | Not supported after a newer supported release is available |

Before the `v1.0.0-rc.1` tag is published, only the release-candidate branch
under review receives fixes. Security fixes may be released without advance
public disclosure when disclosure would put users at risk.

## Report a vulnerability privately

Do not open a public issue, discussion, or pull request for a suspected
vulnerability. Use [GitHub private vulnerability
reporting](https://github.com/NoahJenkins/Unifi-CLI/security/advisories/new).
If that form is unavailable, open an issue containing only a request for
private contact; do not include vulnerability details.

Once a private channel exists, include:

- the affected release and commit;
- operating system and UniFi Network version;
- minimal reproduction steps and impact;
- the smallest redacted evidence needed to validate the report.

Expect an initial acknowledgement within seven days. A report does not
authorize testing third-party controllers or infrastructure. Test only systems
you own or have explicit authorization to assess.

## Never include secrets or controller data

Do not send API keys, WLAN passphrases, credential-store records, controller
configuration exports, raw responses, network inventories, device/client
identifiers, public IPs, or generated live-test reports. Replace real values
with synthetic placeholders and reduce traces to the fields needed to show the
problem.

If a credential is exposed in a report, issue, terminal transcript, shell
history, or repository file, revoke or rotate it immediately and remove the
retained copy where possible. Do not merely redact a public comment and keep
using the credential.

## Security invariants

- API keys belong in the native OS credential store or the explicitly selected
  protected-file fallback, never repository files or command arguments.
- WLAN passphrases use the hidden `--password` prompt or bounded
  `--password-stdin`; they are excluded from plans, JSON, logs, and reports.
- Every mutation is plan-first and requires `--yes`; `--dry-run` always wins.
  Experimental applies also require `--experimental`; high-impact and
  destructive applies require `--force` while `safe_mode` is enabled.
- TLS verification is the default. Prefer a custom `ca_cert`; explicit
  insecure mode permits controller impersonation by an active network
  attacker and cannot be combined with a custom CA.
- Automatic redirects are rejected so API keys and mutation bodies cannot be
  forwarded to another origin.
- Targeted writes bind immutable IDs, revalidate observed state before one
  apply attempt, and fail closed on verification mismatches.
- The normal live integration suite remains read-only. Non-DNS writes may be
  tested only on a dedicated sacrificial controller, never a production-like
  network. Generated evidence must remain redacted and uncommitted.
