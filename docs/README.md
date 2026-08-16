# Documentation Map

Current user and contributor documentation lives at the repository root:

- [`README.md`](../README.md): installation, configuration, commands, safety, and development
- [`SECURITY.md`](../SECURITY.md): vulnerability reporting and security invariants
- [`CONTRIBUTING.md`](../CONTRIBUTING.md): contribution and verification workflow
- [`CHANGELOG.md`](../CHANGELOG.md): release-contract history
- [`compatibility.md`](compatibility.md): controller version and API support status
- [`releases/v1.0.0-rc.2.md`](releases/v1.0.0-rc.2.md): current RC release notes and user checklist
- [`releases/v1.0.0.md`](releases/v1.0.0.md): stable v1 release contract
- [`maintainers/release-checklist.md`](maintainers/release-checklist.md): release and post-merge metadata gates

The files under `docs/superpowers/` are dated design and implementation records. They explain how the project evolved, but they are not current setup instructions. In particular, the original username/password and cookie-session designs were superseded by API-key-only authentication.

| Record | Current status |
|---|---|
| 2026-07-27 initial CLI design and plan | Implemented; authentication sections superseded |
| 2026-07-28 session-persistence design and plan | Superseded; no session/password authentication remains |
| 2026-07-28 read-only live-test design and plan | Implemented; old credential alternatives are superseded |
| 2026-07-29 API-key-only design and plan | Implemented |
| 2026-08-06 GitHub Actions design and plan | Implemented; required-check governance is managed in GitHub settings |
| 2026-08-07 v1 release-candidate plan | Superseded by the 2026-08-15 v1 readiness plan |
| 2026-08-15 v1 readiness plan | In progress; local implementation and qualification precede provider controls and publication |

When a historical record disagrees with root documentation or code, the root documentation and current implementation are authoritative.
