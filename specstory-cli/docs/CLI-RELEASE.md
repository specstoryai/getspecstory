# Releasing SpecStory CLI

## Creating a Release

### Release Steps

1. Merge changes to `main` branch
2. Update `specstory-cli/changelog.md` with release notes
3. Tag and push the release:
   ```zsh
   git tag -a specstory-cli/v1.0.0 -m "Release v1.0.0"
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

Optional: Update the SpecStory CLI dependency version of Intent.

### Update Any Product/Feature Changes in the Documentation

Scan the [SpecStory CLI documentation](https://docs.specstory.com/) and update any product/feature changes in the [documentation](https://github.com/specstoryai/specstory-website/tree/main/content/docs) repository and push updates to `main`.

## What Gets Automated

The release workflow handles everything after you push the tag:

| Job                   | Description                                                 |
|-----------------------|-------------------------------------------------------------|
| `goreleaser`          | Builds binaries for all platforms, creates GitHub release   |
| Release notes         | Extracts changelog section and updates GitHub release notes |
| Slack notification    | Posts release announcement to #clients channel              |
| `update-homebrew-tap` | Updates formula with new version and SHA256 hashes          |

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

Check that the version in `changelog.md` matches the tag format (e.g., `## v1.0.0` for tag `specstory-cli/v1.0.0`).

### Homebrew tap not updated

1. Verify `HOMEBREW_TAP_TOKEN` secret exists and has write access
2. Check the `update-homebrew-tap` job logs for errors
3. Verify all four artifacts were uploaded: `SpecStoryCLI_Darwin_arm64.tar.gz`, `SpecStoryCLI_Darwin_x86_64.tar.gz`, `SpecStoryCLI_Linux_arm64.tar.gz`, `SpecStoryCLI_Linux_x86_64.tar.gz`

### Manual homebrew update

If automation fails, update manually:

```zsh
# Get SHA256 hashes
VERSION="1.0.0"
curl -sL "https://github.com/specstoryai/getspecstory/releases/download/specstory-cli/v${VERSION}/SpecStoryCLI_Darwin_arm64.tar.gz" | shasum -a 256
curl -sL "https://github.com/specstoryai/getspecstory/releases/download/specstory-cli/v${VERSION}/SpecStoryCLI_Darwin_x86_64.tar.gz" | shasum -a 256
curl -sL "https://github.com/specstoryai/getspecstory/releases/download/specstory-cli/v${VERSION}/SpecStoryCLI_Linux_arm64.tar.gz" | shasum -a 256
curl -sL "https://github.com/specstoryai/getspecstory/releases/download/specstory-cli/v${VERSION}/SpecStoryCLI_Linux_x86_64.tar.gz" | shasum -a 256
```

Then update [Formula/specstory.rb](https://github.com/specstoryai/homebrew-tap/blob/main/Formula/specstory.rb) with the new version and hashes.
