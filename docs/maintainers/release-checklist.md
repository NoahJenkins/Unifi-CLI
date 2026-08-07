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
- [ ] Verify searches find no exposed raw-payload flag, classic firewall
      `--ruleset`, network-write `--purpose`, or false stable non-DNS mutation
      claim.
- [ ] Review [Compatibility](../compatibility.md),
      [SECURITY.md](../../SECURITY.md), and the [RC release
      notes](../releases/v1.0.0-rc.1.md).

## Release gates

- [ ] Rebase and ensure hosted CI passes on the unchanged candidate commit.
- [ ] Run GoReleaser 2.17.1 checks and smoke every generated platform artifact.
- [ ] Verify SHA-256 checksums, SBOMs, source archive, and provenance.
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
- [ ] Verify downloaded release artifacts and
      `go install github.com/noahjenkins/unifi-cli/cmd/unifi@v1.0.0-rc.1`.
- [ ] Update compatibility/release notes only with evidence actually produced.
- [ ] Set the GitHub repository description to this exact text:

  `Unofficial CLI for safely managing local UniFi Network controllers. Not affiliated with or endorsed by Ubiquiti.`

- [ ] Recheck the GitHub description after saving it. This is a Task 9
      post-merge GitHub mutation and must not be performed during Task 8.
