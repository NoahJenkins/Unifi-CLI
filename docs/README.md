# Documentation Map

Current user and contributor documentation lives at the repository root:

- [`README.md`](../README.md): installation, configuration, commands, safety, and development
- [`SECURITY.md`](../SECURITY.md): vulnerability reporting and security invariants
- [`CONTRIBUTING.md`](../CONTRIBUTING.md): contribution and verification workflow
- [`CHANGELOG.md`](../CHANGELOG.md): release-contract history
- [`compatibility.md`](compatibility.md): controller version and API support status
- [`releases/v1.1.0.md`](releases/v1.1.0.md): current stable release notes
- [`releases/v1.1.0-rc.1.md`](releases/v1.1.0-rc.1.md): change and qualification record for the candidate that `v1.1.0` promotes
- [`releases/v1.0.0.md`](releases/v1.0.0.md): first stable v1 release contract
- [`maintainers/release-checklist.md`](maintainers/release-checklist.md): release and post-merge metadata gates
- [`maintainers/dependabot-recovery.md`](maintainers/dependabot-recovery.md): dependency update workflows and leftover retired-controller settings

The files under `docs/superpowers/` are dated design and implementation records. They explain how the project evolved, but they are not current setup instructions. In particular, the original username/password and cookie-session designs were superseded by API-key-only authentication.

| Record | Current status |
|---|---|
| 2026-07-27 initial CLI design and plan | Implemented; authentication sections superseded |
| 2026-07-28 session-persistence design and plan | Superseded; no session/password authentication remains |
| 2026-07-28 read-only live-test design and plan | Implemented; old credential alternatives are superseded |
| 2026-07-29 API-key-only design and plan | Implemented |
| 2026-08-06 GitHub Actions design and plan | Implemented; required-check governance is managed in GitHub settings |
| 2026-08-07 v1 release-candidate plan | Superseded by the 2026-08-15 v1 readiness plan |
| 2026-08-15 v1 readiness plan | Implemented; `v1.0.0` was published on 2026-08-18 |
| 2026-08-20 fixed-IP, profiles, and doctor design and plan | Implemented in `v1.1.0`; fixed-IP writes remain experimental |
| 2026-08-29 exact firewall IPv4 and port design and plan | Implemented in `v1.1.0` as experimental; plan checkboxes were not updated, and no live create has succeeded |
| 2026-10-20 Dependabot recovery plan | Superseded; the recovery controller was retired in #64. See [`maintainers/dependabot-recovery.md`](maintainers/dependabot-recovery.md) |
| 2026-10-30 API contract emulator design and plan | Implemented after `v1.1.0`; offline evidence only, not live qualification |

When a historical record disagrees with root documentation or code, the root documentation and current implementation are authoritative.
