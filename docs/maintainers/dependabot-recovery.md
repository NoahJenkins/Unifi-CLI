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

Repair and review use GitHub Copilot with bounded read-only data tools. See
[Copilot dependency repair](../copilot-dependency-repair.md) for authentication,
cost controls and the credential-isolation boundary.

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
   `COPILOT_GITHUB_TOKEN` only in the dispatch environment. Do not put keys in PRs,
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

Flow-style step and reusable-workflow mappings are expanded as data before executable references are inspected. Protected scalar values stay in the scope comparison. Duplicate keys, malformed collections, excessive nesting, and unsupported syntax block the candidate.

Repair snapshots retain authenticated merge-base file content. When main inserts another occurrence of an action, repair guards use a unique ordered match from that ancestor to main to retain the original PR target. Ambiguous matches, rollback, and assigning the update to the inserted occurrence are rejected. The snapshot byte limit remains unchanged.

The 64-file review limit applies to the entire resulting PR. Validation and publication count original PR files plus newly changed repair or generated files. Unchanged generated files do not consume capacity. An oversized candidate is rejected before tests or publication.

Direct repairs retain requested dependency targets relative to the authenticated merge base, including partial grouped updates. Main-baseline adoption also retains targets added only on main, including inserted repeated actions. Both validation and publication enforce these checks. PRs with more than 64 changed files receive a blocked capacity decision before agent reservations; split such updates before retrying.

Workflow Node-version targets belong to the direct `with` input of their owning setup-node step or reusable-workflow job. Explicit step IDs, or names unique across every step when no ID exists, preserve identity when jobs or steps are reordered. Equivalent quoted names and comment variants share the same identity. Duplicate names are allowed only when all such steps have distinct explicit IDs. Anonymous repeated steps require unique ordered ancestor alignment; ambiguous identities block repair. Unrelated environment keys, nested data, and run-script text cannot replace a setup input. Flow mappings use the same parsed representation as block mappings. Block scalar headers accept either indicator order, such as `|2-` and `|-2`. Script contents remain data; scalar step names do not hide following action fields. Missing or ambiguous ancestor evidence blocks adoption; both requested Node updates and main-only Node targets must be retained.

Equal-sized anonymous repeated target sets also require ordered ancestor alignment. Duplicate ancestor targets cannot distinguish a deletion followed by insertion, so those conflict repairs require manual resolution instead of positional reassignment.

Target retention includes standalone `.nvmrc` and `.node-version` files where repository scope permits them. Authenticated ancestor data distinguishes PR-requested targets from untouched dependencies changed or removed on main. Requested targets remain fixed even if main already contains them.

Snapshot fallback for an already-published conflict repair requires one authenticated App publication receipt bound to the current PR head and main SHA. It rechecks retained targets against that repair's original parent before allowing main-side policy adoption or branch refresh. Missing, duplicate, stale, or unusable provenance blocks automation and requires manual resolution; it cannot infer original intent from the repaired head alone.

Release-test pin verification masks action lines in the expanded workflow representation. Flow collections before an action cannot suppress its upstream tag-to-SHA check.

The Go 1.26.10 patch and matching pinned validator image address GO-2026-6809. Native smoke, race, coverage, vulnerability scanning, and the YAML fuzz target passed with this toolchain. The updated container manifest digest was verified against the registry; local container execution still requires the Docker daemon.

UniFi scope permits eligible action-reference updates, but Node-version inputs remain protected, including direct setup-node inputs. The shared target parser inventories Node inputs for retention during authenticated main-baseline adoption; it does not authorize Node-version edits. Standalone Node version files also remain outside UniFi scope. Environment keys, nested data, script text, and inputs to unrelated actions remain protected workflow content. Tests explicitly reject Node-version edits in block and flow mappings while allowing action-pin updates with unchanged Node inputs.

The shared Node-input parser records major wildcard selectors such as `22.x` and `24.x`, including quoted values and flow mappings. Scope validation still applies each repository's existing Node-version policy. Retention rejects fallback that discards a requested wildcard target; UniFi continues to require manual review for Node-version edits.

Inline YAML comments on supported Node inputs remain part of protected workflow content. Plain and quoted selectors are parsed with matching quotes; scope normalization masks only the version value. Comment changes remain blocked, and retention still rejects target rollback. UniFi continues to block Node-version edits.

Action scope normalization preserves ordinary comment text in block and flow layouts. Complete version annotations on immutable pins remain eligible dependency metadata and require upstream tag verification. Literal comments cannot imitate the normalized version marker. Each repository retains its existing rules for tagged and untagged action references.

Direct repairs preserve the complete dependency target set from the PR head, including untouched dependencies and manifests supplied only as context. They cannot add, remove, move, or change dependency targets. Authenticated main-side adoption remains a separate path.

Flow-comment comparison retains the owning node and position relative to collection separators. Version-like comments on non-action mappings and extra version comments remain protected; only the rendered action annotation can normalize as dependency metadata.

### Merge gate lifecycle

When reconciliation resumes after a completed gate, the controller creates a new
App-owned check for the same commit. GitHub retains an existing conclusion when
a completed check is patched, so reusing it can leave task claims blocked. Pending
evaluations reuse the new check until completion. This does not reset the recovery
ledger or authorize a merge without current-head validation and independent review.

GitHub can report an older PR base SHA while the target branch has advanced. When the authenticated comparison proves that main is absent from the PR and GitHub marks the PR mergeable and behind, the controller can use its bounded branch-refresh budget. It checks that the observed PR head/base and current main remain unchanged before the write. This exception permits only branch refresh; merge, review, repair dispatch, and CI retry retain exact-current-base requirements.

### Restricted Copilot runtime

The credential-free offline smoke verifies the real CLI and curated search tool.
See [Copilot dependency repair](../copilot-dependency-repair.md) for current limits and activation requirements. A failed session keeps its reservation consumed.
