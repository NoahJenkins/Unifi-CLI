# Dependabot recovery

The recovery workflow is implemented locally. It is not active until the dedicated
App, protected environments, state issue, merge gate and provider key are configured
and a hosted canary passes.

## Policy and flow

Keep the existing main ruleset: all 13 platform, vulnerability, fuzz and native
keyring checks, strict current-base checks, resolved review threads, and CodeQL.
The controller does not remove these rules or grant bypass. It adds an App-bound
`Dependabot Merge Gate` and a separate review for automated dependency changes.
Patch and minor dependency updates remain eligible. Major updates, controller
wire behavior changes and unsupported file changes require manual review.

PR events, completed CI/Release runs, status events, manual dispatch and a
half-hour schedule reconcile open PRs. Only trusted current-main workflow runs
can reserve a repair. Trusted interrupted CI can be retried; a stale branch can
be refreshed. Each PR has separate lifetime limits of two repairs, two CI retries
and two refreshes. A new commit does not reset the limits. Daily limits are two
per recovery kind and eight independent reviews. Reservations are saved before
side effects; failed, expired and ambiguous operations remain charged. Exhausted
attempts appear in the merge gate and workflow summary. No automatic close occurs.

OpenCode Go uses `kimi-k2.7-code` for repair and a fresh `glm-5.3` session for review.
The pinned OpenCode runtime runs in a container with no provider/GitHub/App key,
external network, shell, subagent or plugin access. A separate broker owns the
provider key and permits only the fixed model/endpoint, 24 repair or 16 review
calls, and bounded request time/size. The canonical endpoint follows the
[OpenCode Go API documentation](https://dev.opencode.ai/v2/docs/console/go).

Repair can change Go dependency versions, a patch-level Go toolchain and exact
release action SHA literals in `release_pipeline_test.go`. It cannot change
workflow commands, workflow files, controller code or other test logic. Test pins
must match the PR's release workflow; changed pins also require verified upstream
tag proof. Unsupported action references fail closed.

A separate secret-free Go container initializes a temporary Git repository and
runs module resolution, the existing release-target prebuild, smoke, race,
coverage and vulnerability checks. The prebuild prepares all six release targets
before the per-fixture five-minute timer starts. It uses the same build routine
and retains the overall 20-minute validation limit and every test. Its only
Git ownership exception is `/workspace`. The publisher verifies the immutable
workflow/run/job, durable claim, artifact hashes, approved file scope, exact PR
head and base, then publishes once with an expected-head check. It does not retry
an ambiguous write. The repaired head needs new hosted CI and independent review.

Before merge, the controller checks all required jobs and checks on the current
head, current-main ancestry, authenticated independent review, main rulesets and
release health. GitHub runs with an empty PR list require an authenticated exact
commit-to-PR association; missing or contradictory proof blocks merge.

## Release failure interlock

The latest trusted Release run must be complete and successful. Missing history,
in-progress work, a failed release or an API read failure stops automatic merges
and cancels observed queued bot merges. Release completion and scheduled scans
recheck this condition. A later successful trusted Release run resolves the
interlock. Release publication and production rollback remain manual.

A GitHub event cannot undo a merge already executing when a release fails. The
required gate is set pending before evaluation and checked again immediately
before release. Keep the gate and strict main ruleset enabled.

## Setup

1. Install a dedicated GitHub App for this repository. Grant Actions write
   (bounded interrupted-run retry), Checks/Contents/Pull requests/Issues write,
   Administration read and Metadata read. Do not grant branch-protection bypass.
   The repair publisher does not need Workflows write.
2. Create `dependabot-controller` and `dependabot-dispatch` environments, restrict
   both to `main`, and disable administrator bypass. Store
   `DEPENDABOT_CONTROLLER_PRIVATE_KEY` only in the controller environment and
   `OPENCODE_GO_API_KEY` only in the dispatch environment. Do not put keys in PRs,
   issues, logs or chat.
3. Create an issue for App-authored state. Set repository variables
   `DEPENDABOT_CONTROLLER_APP_ID` and `DEPENDABOT_STATE_ISSUE_NUMBER`. The token
   action supplies the App slug. Preserve the issue and its reservations/claims.
4. Leave `DEPENDABOT_AUTOMATION_ENABLED` and `DEPENDABOT_AGENT_ENABLED` false while
   publishing the App-owned gate. Add `Dependabot Merge Gate`, bound to that App
   ID, to main's required checks. Retain the 13 existing checks, CodeQL and review
   thread requirements. Enable native squash auto-merge if not already enabled.
5. Confirm provider model access and cost controls, then run a controlled hosted
   canary with both flags set to exact `true`. Verify isolation, failure handling,
   current-head CI/review, one-shot publication and exhausted-attempt reporting.
   Never bypass the gate to make the canary pass.

The App key and provider key are operator-provisioned. Missing App configuration
produces a setup warning on manual/daily runs. Missing credentials or evidence
fails closed. Disabling automation cancels observed queued Dependabot merges;
keep the required gate installed during investigation. Do not delete the state
ledger to reset limits or rerun an ambiguous publication.

## Local checks

Run `node --test .github/scripts/*.test.cjs`, `actionlint`, and all Go gates in
`AGENTS.md`. Use the declared Go toolchain. Go 1.26.9's local VCS discovery requires
a `.git` directory for build-version stamping; a linked worktree's `.git` file
can cause the pre-existing release artifact tests to report an unversioned module.
Use a disposable full local clone for those checks. The validation container
creates a real isolated `.git` directory.

Hosted App publication, paid provider access and platform matrix validation are
separate acceptance steps. Local results do not establish live activation.

### 2026-10-20 verification record

37 controller/broker tests passed under Node 24. Controller and CI actionlint
passed. Native Go 1.26.9 smoke, race, coverage and vulnerability gates passed in a
disposable full clone. Govulncheck found no vulnerabilities; every configured
coverage threshold passed.

The complete secret-free validator command passed using the same pinned image
index on native Linux ARM64 with its two-CPU, 6 GiB, 2 GiB temporary-storage and
20-minute limits. It passed the six-target release prebuild, smoke, race, coverage
and vulnerability checks. The earlier x64 emulation run exposed a cold cross-build
fixture timeout; the fixed prebuild avoids charging that setup to each fixture's
five-minute context. Production remains Linux AMD64. A hosted AMD64 canary and
real App/provider publication remain unverified.

### Recovery provenance

Automatic merge and recovery require controller code from the current `main`
commit. A queued run from an older workflow revision leaves the merge gate
pending and cancels existing automatic merge intent.

CI retry reservations record the authenticated workflow run ID and run attempt.
A later interrupted attempt on the same PR commit can use the second retry, but
a duplicate event cannot repeat a write. The run and attempt are checked again
before dispatch. Old reservations without these fields remain consumed and block
repeat recovery for that head. The two-attempt lifetime and daily limits still
apply. GitHub's accepted, bodyless HTTP 201 rerun response is treated as success.

The scope check compares PR file changes against the authenticated common ancestor of current main and the PR head. This permits bounded refresh when main changed unrelated protected settings. After refresh, the common ancestor must equal current main before merge can proceed. Missing or contradictory comparison evidence blocks the controller.

Each reconciliation reserves at most ten model tasks, matching the claim limit. Excess work waits for another run without consuming a reservation, including when reconciliation crosses UTC midnight. Daily and per-PR limits remain unchanged.

Repair snapshots authorize the PR delta against its authenticated merge base and retain current-main content for conflict repair. Dependency generation runs before the validation checks in a separate secret-free container. The host records the submitted files and generated dependency files before checks, rejects later byte changes, and publishes only those recorded bytes. Both phases share the existing 20-minute execution limit.

For a conflicted file already present in the authenticated PR snapshot, validation and publication may use current-main content as the comparison baseline when the head comparison fails. The same repository file policy must pass against that baseline. This permits preserving main-side settings while retaining dependency updates; it does not authorize new protected edits, unrelated context files, or relaxed action-pin checks.
The controller applies this same comparison after publication so a repaired head can reach bounded branch refresh and fresh current-base CI. Main-side fallback cannot remove an existing immutable action pin.
Unchanged generated dependency files are validated separately from the submitted file set. They do not reduce its 64-file capacity; changed output still obeys the existing 64-file and total-byte limits.

When repair output adopts main-side policy, it must retain every dependency target from the authenticated PR head that differs from main. Validation and publication both enforce this rule, including grouped updates. Copying main must not erase an update, remove its dependency, or partially revert a group. A mutable action tag may adopt main's immutable SHA only for the same version; an existing SHA target must remain exact. Ambiguous or conflicting targets fail closed for operator review. This restriction applies to repair output, not the ancestor-relative comparison used during reconciliation.

Immutable workflow pins are matched by job and action occurrence when adopting main-side content. Comments and job settings do not change that identity, while repeated action steps cannot exchange their protection. Scope checks, upstream verification, and both retention checks share one executable-reference parser. It recognizes quoted references, CRLF line endings, anchored job declarations, and steps whose sequence marker is on its own line. Only direct job-level reusable workflow references and executable step references count; environment and input keys named `uses` are data. Reconciliation permits authenticated main-side comparison for modified files even when the merge-base file is empty.

Existing eligibility remains unchanged: scope checks require immutable references with version tags. Parsing more YAML layouts does not permit mutable tags or relax upstream version checks.
