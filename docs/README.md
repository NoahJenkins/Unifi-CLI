# Documentation Map

Current user and contributor documentation lives at the repository root:

- [`README.md`](../README.md): installation, configuration, commands, safety, and development
- [`SECURITY.md`](../SECURITY.md): vulnerability reporting and security invariants
- [`CONTRIBUTING.md`](../CONTRIBUTING.md): contribution and verification workflow

The files under `docs/superpowers/` are dated design and implementation records. They explain how the project evolved, but they are not current setup instructions. In particular, the original username/password and cookie-session designs were superseded by API-key-only authentication.

| Record | Current status |
|---|---|
| 2026-07-27 initial CLI design and plan | Implemented; authentication sections superseded |
| 2026-07-28 session-persistence design and plan | Superseded; no session/password authentication remains |
| 2026-07-28 read-only live-test design and plan | Implemented; old credential alternatives are superseded |
| 2026-07-29 API-key-only design and plan | Implemented |
| 2026-08-06 GitHub Actions design and plan | Implemented; required-check governance is managed in GitHub settings |

When a historical record disagrees with root documentation or code, the root documentation and current implementation are authoritative.
