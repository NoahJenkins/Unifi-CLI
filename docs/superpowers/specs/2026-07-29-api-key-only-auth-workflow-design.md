# API-Key-Only Persistent Authentication Design

**Date:** 2026-07-29
**Status:** Approved design — awaiting written-spec review

## Goal

Replace UniFi CLI username/password and cookie-session authentication with an
API-key-only workflow that is convenient for interactive users and persists
securely across terminal exits and machine restarts.

## Scope

- Support interactive login with a hidden API-key prompt.
- Persist one API key per normalized controller URL in the platform credential
  store, with an explicit protected-file fallback.
- Retain `UNIFI_API_KEY` for non-interactive automation as a process-only
  override.
- Remove password credentials, API-key YAML configuration, and cookie/CSRF
  session authentication.
- Safely clean up legacy saved cookie-session state.

## Command workflow

### Interactive users

`unifi login` requires an interactive terminal and prompts without echoing the
entered API key. It validates the key using a read-only UniFi request before
persisting it. A successful login reports only the normalized controller URL;
it never displays the key.

```text
$ unifi login
API key: ••••••••
Logged in to https://controller.example:443
```

`unifi logout` deletes the locally saved key for the configured controller. It
does not revoke the key from UniFi. `unifi auth status` remains a read-only
diagnostic command and reports `saved_api_key` or `environment_api_key`, never
the key value.

`unifi login --file-fallback` is the sole opt-in path for saving state to a
protected local file when the native credential store is unavailable. No
command accepts an API key as a flag, preventing shell-history exposure.

### Automation

`UNIFI_API_KEY` remains supported for CI and scripts. It is used only by the
current process and is never written to persistent state. An interactive login
attempt without a terminal fails with guidance to set `UNIFI_API_KEY`.

## Credential resolution and persistence

Runtime resolution order is:

1. `UNIFI_API_KEY` environment override.
2. Saved API key for the normalized controller URL.
3. A `not_authenticated` error that directs the user to `unifi login`.

The normalized controller URL includes its scheme, host, and port. This
isolates credentials for separate controllers. The standard store uses macOS
Keychain, Windows Credential Manager, and Linux Secret Service. The existing
cross-platform state-directory rules, atomic writes, and user-only file modes
are reused for the explicitly requested fallback; because this fallback stores
a long-lived secret, it must remain opt-in and clearly identified in its error
and help text.

On a `401` from a saved key, the CLI removes that saved key and directs the
user to log in again. A `401` produced while `UNIFI_API_KEY` is active does not
modify saved state. A failed validation during `unifi login` never replaces an
existing saved key.

## Configuration and migration

Configuration contains only non-secret controller settings: host, port, TLS
behavior, site, safe mode, and timeout. `username`, `password`, and `api_key`
are removed from the config schema, example config, environment documentation,
and runtime configuration object.

Existing configurations that contain any of those keys fail with a concise
migration message. The message tells users to remove the secret setting and
run `unifi login`; it never echoes its value. This rejects accidental reliance
on plaintext credentials rather than silently continuing to load them.

Legacy saved cookie/CSRF sessions are never read. After a successful API-key
login, and again during logout, the CLI removes the controller's legacy native
store and protected-file entries. The small cleanup path exists only for
deletion; password authentication and session restoration are removed.

## Components

- `internal/config`: defines only non-secret connection settings and detects
  rejected legacy secret fields before they are ignored by YAML decoding.
- `internal/authstore`: owns controller-scoped API-key storage, native-store
  integration, protected-file fallback, and legacy-session deletion.
- `internal/client`: resolves an environment key or saved key, sends it in
  `X-API-KEY`, and invalidates only a failed saved key.
- `internal/cli`: provides the top-level `login` and `logout` commands, hidden
  terminal input, safe status output, and actionable errors.

## Safety constraints

- Never print, serialize to normal config, or accept an API key in a command
  argument or flag.
- Never persist a key before successful read-only validation.
- Require explicit `--file-fallback` before writing a key outside a native
  credential store.
- Scope all state to a normalized controller URL.
- Do not mutate persistent state when using an environment override.
- Retain no usable password-session auth path after the migration.

## Verification

Unit and command tests must cover:

- Hidden interactive prompt behavior and the non-TTY error path.
- Successful login persisting a key and a new client process loading it.
- Controller isolation, logout, and legacy-session cleanup.
- Environment precedence and the guarantee that it neither writes nor deletes
  saved state.
- Invalid login preserving an existing saved key; `401` cleanup only for a
  saved key.
- Native-store unavailable behavior, explicit fallback creation, atomic write
  behavior, and user-only fallback permissions.
- Configuration rejection of all three legacy secret fields and redaction of
  every success or error output.
