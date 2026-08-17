# Maintainer release checklist

This checklist covers `v1.0.0-rc.2` qualification and unchanged-source
promotion to `v1.0.0`. Documentation work must
not mutate GitHub, tag a commit, publish a release, or contact a controller.

## Pre-merge

- [ ] Confirm the branch contains no API keys, WLAN secrets, credential-store
      exports, controller payloads, live identifiers, generated reports, or
      binaries.
- [ ] Confirm the exact unofficial-project disclaimer appears in README, root
      CLI help, and RC release notes.
- [ ] Run formatting, vet, full unit, race, coverage, vulnerability, link,
      Markdown link/command, help, version, and schema checks.
- [ ] Run every fuzz target for 30 seconds and run
      `UNIFI_RELEASE_HOST=1 ./scripts/check-performance.sh` on the designated
      darwin/arm64 host. Record the three-sample medians in the RC.2 notes.
- [ ] Run the native keyring round trip on macOS, Windows, and Linux Secret
      Service. Confirm each unique synthetic entry was deleted.
- [ ] Run `go test . -run Release -count=1` to lint the GoReleaser and release
      workflow contract, then `go run ./cmd/release-smoke --all` to cross-build
      and structurally inspect the exact six release targets. Non-native targets
      are not executed by this command; their output states that limitation.
- [ ] Verify searches find no exposed raw-payload flag, classic firewall
      `--ruleset`, network-write `--purpose`, or false stable non-DNS mutation
      claim.
- [ ] Review [Compatibility](../compatibility.md),
      [SECURITY.md](../../SECURITY.md), and the [RC release
      notes](../releases/v1.0.0-rc.2.md).

## Release gates

- [ ] Rebase and ensure hosted CI passes on the unchanged candidate commit.
- [ ] Run GoReleaser 2.17.1 checks and smoke every generated platform artifact.
- [ ] In an isolated job that never runs Syft or GoReleaser, cross-build all six
      trusted binaries from the exact checkout with the release version,
      commit, and commit-date linker values; generate an exact path, Git-mode,
      and SHA-256 source manifest; seal those inputs; and expose the seal digest
      as a job output. Generate and seal GoReleaser output in a separate job.
      A third read-only job must validate both digests as exactly 64 lowercase
      hexadecimal characters, verify both seals, safely extract the bundles
      into disjoint `trusted` and `generated` roots, and reject archive members
      outside each bundle's allowlisted namespace before comparing generated
      artifacts with the isolated trusted inputs. Confirm its
      `go run ./cmd/release-smoke --artifacts ... --expected-version "${RELEASE_TAG#v}" --expected-commit "$RELEASE_COMMIT" --trusted-binaries ... --trusted-source-manifest ...`
      verifies the source archive, all six exact archive names and layouts,
      their exhaustive SHA-256 entries, bound CycloneDX SBOMs, embedded build
      settings, byte-for-byte equality between every archived executable and
      its corresponding trusted cross-build, exact source-manifest equality,
      and trusted Git hashes for LICENSE, README.md, and CHANGELOG.md inside
      every platform archive before provenance attestation. The primary
      artifact-set verifier does not execute untrusted archive content; after
      equality is proven, the six native runner jobs extract their matching
      archive and run the same four-command executable contract before release.
- [ ] Confirm GoReleaser runs with `--skip=publish`, so artifact generation has
      no contents-write, attestation, or OIDC authority. Every transferred tar
      must match the producing job's SHA-256 output before extraction. Reverify
      the sealed bundle before the six native runner smokes, attestation, and
      publication staging. Pass all cross-job digests through environment
      variables and validate them before comparison; never interpolate them
      into shell source. The final contents-write job must contain no
      third-party actions and must never execute code supplied by an artifact:
      it fetches the exact `RELEASE_COMMIT`, safely extracts the data-only
      publication bundle, independently rebuilds all six trusted binaries and
      the source manifest, reruns the complete artifact verifier, then executes
      only the publisher from that exact commit. It uploads only the checksum
      allowlist, downloads every draft asset by ID, and compares exact bytes
      before making the release public. Any mismatch must leave a draft.
- [ ] Confirm the exact-tag preflight binds `RELEASE_TAG` to
      `RELEASE_COMMIT` and
      runs before Syft and GoReleaser and again immediately before publication.
      A missing
      release or an existing draft may proceed; an existing published release,
      malformed response, repository-access failure, authentication failure,
      or unexpected API error must stop the workflow before release mutation.
      Concurrent runs for the same repository and exact ref are serialized and
      never cancel an in-progress release run.
- [ ] If a release run fails before publication, fix and merge the workflow;
      never move or replace the existing tag. Resume the complete workflow
      manually with the existing tag and its exact 40-character commit. The
      resume path must pass the same preflight, independent builds, bundle
      comparisons, six native artifact smokes, provenance, and publisher gates
      as a tag-triggered run. Its signed SLSA predicate must identify both the
      immutable tag commit as the release source and the newer workflow commit
      that defines the recovery build; default dispatch provenance alone names
      only the workflow commit and is insufficient.
- [ ] Verify SHA-256 checksums, provenance, the source archive's exact full-commit
      PAX binding, every SBOM's single exact archived-executable file component,
      all reported SHA-1/SHA-256 values, and an exact library/version inventory
      derived from each independently built trusted binary. Reject unrelated
      file claims and setuid, setgid, and sticky archive entries.
- [ ] Run the authenticated read-only live suite with verified TLS, or record
      the explicit `insecure: true` compatibility exception when the authorized
      local controller has a self-signed certificate.
- [ ] Record the actual UniFi Network version used. If 10.4.57 is not tested,
      leave its compatibility status explicitly unverified.
- [ ] After separate explicit approval, run disabled and uniquely named
      lifecycles for A, AAAA, CNAME, MX, TXT, SRV, and forwarded-domain DNS
      policies. Capture every created ID, delete only those IDs, and prove the
      exact baseline is restored.
- [ ] Do not run non-DNS mutations unless a separate dedicated sacrificial
      controller with disposable configuration is available and explicitly
      approved. Never use a production-like controller.

## Publish and post-merge

- [ ] Confirm the release uses the authenticated NoahJenkins GitHub account and
      no release signing key. Record that this is account-controlled, not a
      cryptographically signed or independently approved tag.
- [ ] Merge the reviewed PR after hosted checks.
- [ ] Verify the account-only `v*` ruleset blocks tag creation, update,
      deletion, and non-fast-forward changes except for the NoahJenkins
      account.
- [ ] Verify the protected `release` environment requires the configured
      manual approval and that only the publisher job has write authority.
- [ ] Create and verify the account-controlled `v1.0.0-rc.2` tag on the unchanged protected
      main commit. Confirm the workflow publishes that exact verified draft.
- [ ] Verify downloaded release artifacts and
      `go install github.com/noahjenkins/unifi-cli/cmd/unifi@v1.0.0-rc.2`.
- [ ] Confirm the tagged source install reports `v1.0.0-rc.2` while `commit`
      and `build_date` remain `unknown`. Confirm verified archives report
      authoritative full metadata and have matching checksums, CycloneDX SBOMs,
      and provenance.
- [ ] If every RC.2 gate passes, create account-controlled `v1.0.0` on the same unchanged
      source commit. Verify `prerelease=false`, stable notes, installations,
      checksums, SBOMs, provenance, and final remote tag/rule state.
- [ ] Update compatibility/release notes only with evidence actually produced.
- [ ] Set the GitHub repository description to this exact text:

  `Unofficial CLI for safely managing local UniFi Network controllers. Not affiliated with or endorsed by Ubiquiti.`

- [ ] Recheck the GitHub description after saving it. This provider mutation
      must not happen during local candidate verification.
