# Cross-Platform Session Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Reuse authenticated UniFi sessions securely across CLI processes on macOS, Windows, and Linux.

**Architecture:** A session package serializes controller-scoped cookies and CSRF state to an OS keyring, with an explicitly permitted protected-file fallback. The HTTP client hydrates and invalidates that state; Cobra commands expose login, status, and logout behavior.

**Tech Stack:** Go 1.26, Cobra, `github.com/zalando/go-keyring`, Go `net/http` cookie jars.

## Global Constraints

- Persist session cookies and CSRF tokens only; never persist passwords or API keys.
- Prefer the native OS keyring; use a file only when `auth login --file-fallback` is explicitly supplied and the keyring is unavailable.
- Scope stored data by normalized controller URL and do not print secret material.
- Retain API-key precedence and existing YAML/environment credential support.

---

### Task 1: Session persistence package

**Files:** Create `internal/session/store.go`, `internal/session/store_test.go`; modify `go.mod`, `go.sum`.

Implement a controller-scoped `Session` model plus a `Store` interface, keyring-backed store, and atomic `0600` file fallback. Cover serialization, controller isolation, keyring preference, opt-in fallback, deletion, and permissions with tests.

### Task 2: Client hydration and invalidation

**Files:** Modify `internal/client/client.go`, `internal/client/auth.go`, `internal/client/client_test.go`.

Inject the session store into the client, restore saved cookies/CSRF state at construction, save successful password logins, and clear stale state after a `401`. Preserve API-key precedence and test restored-session requests end to end.

### Task 3: Authentication commands and documentation

**Files:** Modify `internal/cli/auth.go`, `internal/cli/context.go`, `internal/cli/cli_test.go`, `README.md`; add command tests.

Add `auth login --file-fallback`, read-only `auth status`, and local `auth logout`; report the selected auth method without secrets. Document cross-platform stores, Linux Secret Service, fallback behavior, and removal.

### Task 4: Full verification and release documentation

**Files:** Modify this plan/design only if review identifies a documentation gap.

Run `go test ./...`, build the CLI, inspect secret-redaction paths, review the full diff, and commit the approved design, plan, implementation, tests, and README together.
