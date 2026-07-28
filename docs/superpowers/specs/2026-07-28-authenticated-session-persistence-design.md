# Cross-Platform Session Persistence Design

**Date:** 2026-07-28  
**Status:** Approved for implementation  

## Goal

Allow the `unifi` CLI to reuse an authenticated UniFi controller session on
macOS, Windows, and Linux without writing a password or API key.

## Design

`internal/session` owns a small serializable session record containing the
controller URL, request cookies, and CSRF token. The record is scoped to the
normalized controller URL by a SHA-256-derived account key. It is stored in
the operating system's credential vault through `github.com/zalando/go-keyring`.

When no usable keyring exists, `unifi auth login --file-fallback` may save the
same record in a dedicated state file. The file is never a YAML config or
`.env` file, contains no long-lived credentials, lives in a user-only
directory, and is written atomically with mode `0600`.

The HTTP client restores the record before its first request. A `401` removes
the stale record and reports `auth_failed` with a login hint. API-key auth
continues to take precedence and never accesses the session store.

## Command behavior

- `unifi auth login` creates and saves a new session.
- `unifi auth login --file-fallback` permits the protected-file fallback only
  if the keyring is unavailable.
- `unifi auth status` validates the configured credential/session path with a
  read-only sites request and reports `saved_session`, `password`, or `api_key`.
- `unifi auth logout` removes local persisted sessions; it does not call a
  controller logout endpoint.

## Safety constraints

- Never print cookie values, CSRF tokens, passwords, or API keys.
- Never silently write session material to disk.
- Saved sessions are bound to one scheme, host, and port.
- A missing Linux Secret Service produces setup guidance unless the caller
  explicitly requested the file fallback.
