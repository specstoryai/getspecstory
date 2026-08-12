# Releasing SpecStory CLI

## Creating a Release

### Release Steps

1. Merge changes to `main` branch
2. Update `specstory-cli/changelog.md` with release notes
3. Tag and push the release:
   ```zsh
   git tag -a specstory-cli/v2.0.0 -m "Release v2.0.0"
   git push origin --tags
   ```
4. Monitor the [GitHub Action](https://github.com/specstoryai/getspecstory/actions)
5. Verify the [GitHub Release](https://github.com/specstoryai/getspecstory/releases) has correct changelog
6. Verify [homebrew-tap](https://github.com/specstoryai/homebrew-tap) was updated

### Verification

After release, verify the Homebrew installation works:

```zsh
brew tap specstoryai/homebrew-tap
brew update
brew upgrade specstory
specstory version
```

Verify the Windows build (v2.9.0 and later). On a Windows machine, download `SpecStoryCLI_Windows_x86_64.zip` from the release, then:

```powershell
Expand-Archive SpecStoryCLI_Windows_x86_64.zip -DestinationPath .
.\specstory.exe version
```

Or spot-check the artifact from macOS/Linux without a Windows machine:

```zsh
VERSION="2.9.0"
curl -sLO "https://github.com/specstoryai/getspecstory/releases/download/specstory-cli/v${VERSION}/SpecStoryCLI_Windows_x86_64.zip"
unzip -o SpecStoryCLI_Windows_x86_64.zip && file specstory.exe   # expect: PE32+ executable
```

Optional: Update the SpecStory CLI dependency version of Intent.

### Update Any Product/Feature Changes in the Documentation

Scan the [SpecStory CLI documentation](https://docs.specstory.com/) and update any product/feature changes in the [documentation](https://github.com/specstoryai/specstory-website/tree/main/content/docs) repository and push updates to `main`.

## What Gets Automated

The release workflow handles everything after you push the tag:

|          Job          |                                Description                                |
| --------------------- | ------------------------------------------------------------------------- |
| `goreleaser`          | Builds binaries for all platforms (incl. Windows), creates GitHub release |
| Release notes         | Extracts changelog section and updates GitHub release notes               |
| Slack notification    | Posts release announcement to #clients channel                            |
| `update-homebrew-tap` | Updates formula with new version and SHA256 hashes                        |

### Release Artifacts

Each release publishes ten archives, each containing just the `specstory` binary:

|  Format  |                                       Platforms                                       |
| -------- | ------------------------------------------------------------------------------------- |
| `tar.gz` | Darwin_arm64, Darwin_x86_64, Linux_arm64, Linux_x86_64                                |
| `zip`    | Darwin_arm64, Darwin_x86_64, Linux_arm64, Linux_x86_64, Windows_arm64, Windows_x86_64 |

Windows ships zip-only per platform convention; the Unix tar.gz artifacts are what Homebrew and `install.sh` consume.

### Windows Distribution (current state)

- **Install channel:** direct download of the zip from the GitHub release only — there is no winget or Scoop package yet (`install.sh` covers WSL, which installs the Linux binary). The docs site's install instructions must say this.
- **Unsigned binaries:** the Windows executables are not Authenticode-signed, so first run shows a SmartScreen "unrecognized app" warning, and Go binaries occasionally trip Defender heuristics. Code signing is planned future work.
- **Test coverage:** CI cross-compiles the Windows (and Darwin) targets and runs the full test suite natively on a `windows-latest` runner. The Windows job reports failures like any other.

## Required Secrets

| Secret                      | Purpose                                             |
|-----------------------------|-----------------------------------------------------|
| `GITHUB_TOKEN`              | Built-in token for release creation                 |
| `POSTHOG_API_KEY`           | Analytics key embedded in binaries                  |
| `SLACK_WEBHOOK_URL_CLIENTS` | Slack notification webhook                          |
| `HOMEBREW_TAP_TOKEN`        | PAT with write access to `specstoryai/homebrew-tap` |

## Repositories

- **Source**: [specstoryai/getspecstory](https://github.com/specstoryai/getspecstory) - CLI source code and releases
- **Homebrew tap**: [specstoryai/homebrew-tap](https://github.com/specstoryai/homebrew-tap) - Homebrew formula

## Troubleshooting

### Release notes not updated

Check that the version in `changelog.md` matches the tag format (e.g., `## v2.0.0` for tag `specstory-cli/v2.0.0`).

### Missing release artifacts

Verify all ten archives listed under [Release Artifacts](#release-artifacts) were uploaded. A missing Windows zip fails no downstream job (nothing consumes it automatically), so it must be caught by eye here.

### Homebrew tap not updated

1. Verify `HOMEBREW_TAP_TOKEN` secret exists and has write access
2. Check the `update-homebrew-tap` job logs for errors
3. Verify the four tar.gz artifacts the formula consumes were uploaded: `SpecStoryCLI_Darwin_arm64.tar.gz`, `SpecStoryCLI_Darwin_x86_64.tar.gz`, `SpecStoryCLI_Linux_arm64.tar.gz`, `SpecStoryCLI_Linux_x86_64.tar.gz`

### Manual homebrew update

If automation fails, update manually:

```zsh
# Get SHA256 hashes
VERSION="2.0.0"
curl -sL "https://github.com/specstoryai/getspecstory/releases/download/specstory-cli/v${VERSION}/SpecStoryCLI_Darwin_arm64.tar.gz" | shasum -a 256
curl -sL "https://github.com/specstoryai/getspecstory/releases/download/specstory-cli/v${VERSION}/SpecStoryCLI_Darwin_x86_64.tar.gz" | shasum -a 256
curl -sL "https://github.com/specstoryai/getspecstory/releases/download/specstory-cli/v${VERSION}/SpecStoryCLI_Linux_arm64.tar.gz" | shasum -a 256
curl -sL "https://github.com/specstoryai/getspecstory/releases/download/specstory-cli/v${VERSION}/SpecStoryCLI_Linux_x86_64.tar.gz" | shasum -a 256
```

Then update [Formula/specstory.rb](https://github.com/specstoryai/homebrew-tap/blob/main/Formula/specstory.rb) with the new version and hashes.
