# Maintainer release checklist

This checklist is for Task 9 delivery of `v1.0.0-rc.1`. Documentation work must
not mutate GitHub, tag a commit, publish a release, or contact a controller.

## Pre-merge

- [ ] Confirm the branch contains no API keys, WLAN secrets, credential-store
      exports, controller payloads, live identifiers, generated reports, or
      binaries.
- [ ] Confirm the exact unofficial-project disclaimer appears in README, root
      CLI help, and RC release notes.
- [ ] Run formatting, vet, full unit, race, coverage, vulnerability, link,
      Markdown link/command, help, version, and schema checks.
- [ ] Run `go test . -run Release -count=1` to lint the GoReleaser and release
      workflow contract, then `go run ./cmd/release-smoke --all` to cross-build
      and structurally inspect the exact six release targets. Non-native targets
      are not executed by this command; their output states that limitation.
- [ ] Verify searches find no exposed raw-payload flag, classic firewall
      `--ruleset`, network-write `--purpose`, or false stable non-DNS mutation
      claim.
- [ ] Review [Compatibility](../compatibility.md),
      [SECURITY.md](../../SECURITY.md), and the [RC release
      notes](../releases/v1.0.0-rc.1.md).

## Release gates

- [ ] Rebase and ensure hosted CI passes on the unchanged candidate commit.
- [ ] Run GoReleaser 2.17.1 checks and smoke every generated platform artifact.
- [ ] Before GoReleaser, cross-build all six trusted binaries from the exact
      checkout with the release version, commit, and commit-date linker values;
      generate an exact path, Git-mode, and SHA-256 source manifest from the
      release commit; and keep all trusted inputs outside `dist`. In the tag
      workflow, confirm
      `go run ./cmd/release-smoke --artifacts dist --expected-version "${GITHUB_REF_NAME#v}" --expected-commit "$GITHUB_SHA" --trusted-binaries "$RUNNER_TEMP/unifi-trusted" --trusted-source-manifest "$RUNNER_TEMP/unifi-source-manifest.json"`
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
      no contents-write, attestation, or OIDC authority. The transferred bundle
      must be reverified before the six native runner smokes, reverified again
      before attestation and publication, and uploaded only by the final
      contents-write job. That job must download every exact-tag draft asset by
      asset ID and compare its bytes with the verified local file before making
      the release public. Any mismatch must leave the release as a draft.
- [ ] Confirm the exact-tag preflight runs before Syft and GoReleaser. A missing
      release or an existing draft may proceed; an existing published release,
      malformed response, repository-access failure, authentication failure,
      or unexpected API error must stop the workflow before release mutation.
      Concurrent runs for the same repository and exact ref are serialized and
      never cancel an in-progress release run.
- [ ] Verify SHA-256 checksums, provenance, the source archive's exact full-commit
      PAX binding, every SBOM's exact archived-executable path and digest, and
      an exact library/version inventory derived from each independently built
      trusted binary. Reject setuid, setgid, and sticky archive entries.
- [ ] Run the authenticated read-only live suite with verified TLS.
- [ ] Record the actual UniFi Network version used. If 10.4.57 is not tested,
      leave its compatibility status explicitly unverified.
- [ ] Run only the approved isolated DNS A-record lifecycle on the authorized
      controller and verify cleanup.
- [ ] Do not run non-DNS mutations unless a separate dedicated sacrificial
      controller with disposable configuration is available and explicitly
      approved. Never use a production-like controller.

## Publish and post-merge

- [ ] Merge the reviewed PR after hosted checks.
- [ ] Tag the unchanged merge commit `v1.0.0-rc.1`; do not create `v1.0.0`.
      Confirm the workflow publishes that exact verified draft and no other
      release.
- [ ] Verify downloaded release artifacts and
      `go install github.com/noahjenkins/unifi-cli/cmd/unifi@v1.0.0-rc.1`.
- [ ] Update compatibility/release notes only with evidence actually produced.
- [ ] Set the GitHub repository description to this exact text:

  `Unofficial CLI for safely managing local UniFi Network controllers. Not affiliated with or endorsed by Ubiquiti.`

- [ ] Recheck the GitHub description after saving it. This is a Task 9
      post-merge GitHub mutation and must not be performed during Task 8.
