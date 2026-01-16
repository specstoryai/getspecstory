# Releasing SpecStory Extension for Claude Code

The following steps are required to release a new version of the SpecStory Extension for Claude Code.

This is as-of January 16, 2026. Much of this will be automated in the future.

## Prepare the Release

Once you have the `dev` branch where you want it for a release, merge the branch into `main`.

## Tag the Release

Once the `main` branch is ready for a release, tag the release with the version number. Update the version number in the tag message to the new version number:

```zsh
git tag -a v0.18.0 -m "Release v0.18.0"
git push origin v0.18.0
```

This will trigger the [GitHub Action](../../.github/workflows/release.yml) to build the release and push it to the [GitHub Releases page](https://github.com/specstoryai/getspecstory/releases).

The steps the GitHub Action will take are:

1. Build binaries for all platforms using GoReleaser
2. Create a GitHub release with the built artifacts
3. Notify Slack of the release

You can monitor the [GitHub Action's progress](https://github.com/specstoryai/getspecstory/actions).

## Check the Release

Go to the [GitHub Releases page](https://github.com/specstoryai/getspecstory/releases) of the `specstoryai/getspecstory` repository and check that the release is there.

## Update the Changelog

Click the pencil icon to edit the changelog for the new release. Copy the changelog from the [changelog.md](../changelog.md) file and paste it into the changelog.

## Get the SHA256 Hashes of the Released Binaries

Update the version number in the URLs to the new version number:

```zsh
curl -sL https://github.com/specstoryai/getspecstory/releases/download/v0.18.0/SpecStoryCLI_Darwin_arm64.tar.gz | shasum -a 256
curl -sL https://github.com/specstoryai/getspecstory/releases/download/v0.18.0/SpecStoryCLI_Darwin_x86_64.tar.gz | shasum -a 256
curl -sL https://github.com/specstoryai/getspecstory/releases/download/v0.18.0/SpecStoryCLI_Linux_arm64.tar.gz | shasum -a 256
curl -sL https://github.com/specstoryai/getspecstory/releases/download/v0.18.0/SpecStoryCLI_Linux_x86_64.tar.gz | shasum -a 256
```

This will give you the SHA256 hashes of the released binaries. e.g.:

```
ae450eedf178706773826be43824bab7a90723c00b2165e3883d59da745f0de6  -
f89e987151add16939ba78cc79b27c3c6aa08e7357ed47712acfd3c2843e1d94  -
fad9d81ab07102e47da37004166ff687923ec3a6fb0a250a60315c36d0f10b01  -
30501cb6196d4bbc39ea2bf6834500d07063db20a42fe060090dcdbeef589cf2  -
```

## Update the Homebrew Formula

Update the version number in the Homebrew formula to the new version number.

Update the [Homebrew formula](https://github.com/specstoryai/homebrew-tap/blob/main/Formula/specstory.rb) in the [specstoryai/homebrew-tap](https://github.com/specstoryai/homebrew-tap) repository with the new SHA256 hashes you got from the cURL commands above.

The 4 URLs in the Homebrew formula are in the same order as the 4 binaries you got the SHA256 hashes for above.

Click the pencil icon to edit the Homebrew formula. Update the 4 URLs in the Homebrew formula with the new URLs you got from the cURL commands above.

Click the "Commit changes" button to commit the changes to the Homebrew formula.

Change the commit message to something like "Release v0.18.0" and click the "Commit changes" button to commit the changes to the Homebrew formula.

## Verify Update Notice in Old Version

Run your existing version of the CLI and verify that you get a notice that the new version is available.

```zsh
specstory version
```

## Verify the Homebrew Release

Upgrade the CLI to the new version:

```zsh
brew tap specstoryai/homebrew-tap
brew update
brew upgrade specstory
```

This should show the new version number:

```zsh
specstory version
```

## Update the Product Documentation

### Update the Version Number for Linux Download in the Documentation

Update the version number in the Linux download section of the [Mintlify documentation](https://dashboard.mintlify.com/specstory/specstory/editor/main) to the new version number.

Navigate to `./specstory/introduction.mdx` in the [Mintlify UI](https://dashboard.mintlify.com/specstory/specstory/editor/main) and scroll to the "SpecStory CLI for Terminal Agents" section, and the "Download Linux/WSL Binary" tab.

Update the version number in the "Linux (ARM64)" and "Linux (x86)" links.

### Update Any Product/Feature Changes in the Documentation

Scan the `./specstory` documentation in the [Mintlify documentation](https://dashboard.mintlify.com/specstory/specstory/editor/main) and update any product/feature changes in the documentation.

### Publish the Documentation

Click the "Publish" button in the top right corner of the [Mintlify UI](https://dashboard.mintlify.com/specstory/specstory/editor/main) to publish the documentation changes.
