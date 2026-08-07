# Documentation Map

Current user and contributor documentation lives at the repository root:

- [`README.md`](../README.md): installation, configuration, commands, safety, and development
- [`SECURITY.md`](../SECURITY.md): vulnerability reporting and security invariants
- [`CONTRIBUTING.md`](../CONTRIBUTING.md): contribution and verification workflow
- [`CHANGELOG.md`](../CHANGELOG.md): release-contract history
- [`compatibility.md`](compatibility.md): controller version and API support status
- [`releases/v1.0.0-rc.1.md`](releases/v1.0.0-rc.1.md): RC release notes and user checklist
- [`maintainers/release-checklist.md`](maintainers/release-checklist.md): release and post-merge metadata gates

The files under `docs/superpowers/` are dated design and implementation records. They explain how the project evolved, but they are not current setup instructions. In particular, the original username/password and cookie-session designs were superseded by API-key-only authentication.

| Record | Current status |
|---|---|
| 2026-07-27 initial CLI design and plan | Implemented; authentication sections superseded |
| 2026-07-28 session-persistence design and plan | Superseded; no session/password authentication remains |
| 2026-07-28 read-only live-test design and plan | Implemented; old credential alternatives are superseded |
| 2026-07-29 API-key-only design and plan | Implemented |
| 2026-08-06 GitHub Actions design and plan | Implemented; required-check governance is managed in GitHub settings |
| 2026-08-07 v1 release-candidate plan | In progress; Tasks 1-8 implemented on the candidate branch, Task 9 delivery remains |

When a historical record disagrees with root documentation or code, the root documentation and current implementation are authoritative.
