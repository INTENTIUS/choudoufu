// chant release: how choudoufu cuts and publishes a release. Run the
// "chant release" workflow from the Actions tab (scripts/cut-release.sh
// dispatch vX.Y.Z does it) with the label to cut; chant tags it v<version>
// and attaches the files below to the GitHub release.
// .github/workflows/chant-release.yml is generated from this file: edit here
// and run `npx chant release workflow`.
export default {
  release: {
    tag: "v{version}",
    // Releases before chant's are tagged v0.x.y, with no ledger entry.
    legacyTags: ["v"],
    green: "forge-checks",
    workflow: {
      trigger: "dispatch",
      // scripts/release-notes.sh writes dist/release-notes.md for the notes
      // below, and fails the cut when CHANGELOG.md has no section for the tag.
      build: "bash scripts/release-notes.sh",
      // The cut job's doctor reports false failures until the next chant release.
      doctor: false,
    },
    artifacts: [
      {
        name: "binaries",
        build: "bash scripts/release-build.sh",
        outputs: ["dist/{os}_{arch}/choudoufu{ext}"],
        platforms: [
          "linux/amd64",
          "linux/arm64",
          "darwin/amd64",
          "darwin/arm64",
          "windows/amd64",
          "windows/arm64",
        ],
        // choudoufu_v0.34.1_linux_amd64.tar.gz, and .zip on Windows.
        archive: { name: "choudoufu_v{version}_{os}_{arch}" },
      },
      { name: "checksums", checksums: "dist/SHA256SUMS", of: ["binaries"] },
    ],
    publish: [
      {
        type: "forge",
        forge: "github",
        repo: "INTENTIUS/choudoufu",
        assets: ["artifact:binaries", "artifact:checksums"],
        // No SBOM: the old release attached none.
        sbom: false,
        notes: {
          file: "dist/release-notes.md",
          heading: "## choudoufu {tag}",
          generate: "forge",
        },
      },
    ],
  },
};
