# Dependabot recovery implementation plan

User approved this scope on 2026-10-20.

Goal: recover eligible dependency PRs automatically without changing release or controller behavior.

Architecture: adapt the existing bounded OpenCode controller from noahjenkins.com-v2 d33fbae; use main-only credential environments, two lifetime repair reservations, independent review, fresh CI and an App-bound merge gate. Go validation runs in a credential-free container. Existing rulesets remain authoritative.

- [x] Add regression coverage for Go patch/minor scope, major refusal, exact release pin synchronization, review and CI provenance, and failed release interlock.
- [x] Adapt policy, source context, fixed Go validation and ruleset inspection. Do not permit runtime controller source changes or arbitrary test edits.
- [x] Connect scheduled/event recovery and bounded safe reads; preserve no-retry publication and lifetime quotas.
- [x] Run controller tests, actionlint and repository Go checks. Document deployment/activation prerequisites and test evidence.

Review focus: stale head/base, forged checks, changed release commands, exhausted budgets, stale successful release status.

## Review and verification

Independent review reproduced four blockers: validator Git ownership, baseline
release-pin inventory, workflow files allowed as repair output, and empty release
history accepted as healthy. Each has a failing-first regression and a fix.
Additional release checks bind the exact workflow, source repository, current-main
ancestry and real release tag. The read-only live release adapter passed.

37 controller/broker tests pass. Controller and CI actionlint pass. Existing
Release shell code has one unchanged SC2129 warning. The three added version
comments were verified against official tag commits, including annotated tags.
Go release-workflow tests pass with Go 1.26.9. Native full-clone Go gates and the complete secret-free ARM64 validator passed;
final evidence and the hosted AMD64 boundary are in the maintainer setup document.

No commit, push, PR, activation, key change, paid provider call or live controller
write was performed. The App, state issue, required gate, private key and valid
OpenCode Go key remain operator prerequisites before a hosted canary.
