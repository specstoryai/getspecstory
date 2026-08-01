# Releasing SpecStory for Mac

How app updates work today, and the path to Sparkle-powered automatic updates with GitHub-only distribution.

## Today: the interim updater

Dev builds are unsigned, so automatic in-place updates are off the table (Gatekeeper would reject them, and shipping unsigned self-updates is not acceptable security-wise). Until signing lands, the app has a manual check:

- Settings > About > "Check for app updates" queries the GitHub releases of `specstoryai/getspecstory` for tags prefixed `mac-app-v` and compares against `CFBundleShortVersionString`. A newer tag opens the releases page for a manual download.
- No `mac-app-v*` tag exists yet, so the check reports the build as current.

The CLI has its own freshness check in Settings (Command line tool section): it compares the user's PATH-installed `specstory` against the latest plain `vN.N.N` release and offers the documented installer (`install.sh` from this repo, the same flow docs.specstory.com prescribes) in the terminal. The app itself never depends on the installed CLI; the bundled sha-pinned binary is the engine.

## Sparkle with GitHub-only distribution: yes, it works

Sparkle needs three things, none of which require dedicated hosting:

1. **A signed, notarized app.** Developer ID certificate, hardened runtime, notarization. This is the real prerequisite and the only missing piece. The vendored CLI binaries inside `Resources/bin` must be signed as part of the bundle.
2. **An appcast feed at a stable HTTPS URL.** GitHub serves this fine:
   - `https://raw.githubusercontent.com/specstoryai/getspecstory/mac-app-releases/appcast.xml` (a dedicated branch), or
   - GitHub Pages, or
   - a `latest` release asset with a stable URL.
   Set it as `SUFeedURL` in Info.plist.
3. **Signed update archives.** Sparkle uses its own EdDSA signature over each archive (independent of Apple signing), generated once with `generate_keys` and applied per release with `sign_update`. The public key ships in Info.plist (`SUPublicEDKey`); the archives themselves are ordinary GitHub release assets referenced by absolute URL from the appcast.

### Release flow (once signing exists)

```zsh
# one time
Sparkle/bin/generate_keys                     # keep the private key in the keychain/CI secret

# per release
./scripts/vendor-cli.sh
xcodegen generate
xcodebuild -project SpecStory.xcodeproj -scheme SpecStory -configuration Release \
  -derivedDataPath build/DerivedData build
codesign --deep --options runtime --sign "Developer ID Application: ..." SpecStory.app
xcrun notarytool submit SpecStory.zip --wait && xcrun stapler staple SpecStory.app
ditto -c -k --keepParent SpecStory.app SpecStory-<version>.zip
Sparkle/bin/sign_update SpecStory-<version>.zip   # emits the edSignature attribute
# append an <item> to appcast.xml (version, minimumSystemVersion, enclosure URL
#   pointing at the GitHub release asset, sparkle:edSignature, length)
gh release create mac-app-v<version> SpecStory-<version>.zip
git push origin mac-app-releases                  # publish the updated appcast
```

### App integration (small, when ready)

- Add the Sparkle SPM package to the app target only (SpecStoryKit stays dependency-free).
- `SPUStandardUpdaterController` owned by the app delegate; "Check for updates" in Settings and the account menu calls `checkForUpdates`.
- `SUFeedURL` + `SUPublicEDKey` in Info.plist; `SUEnableAutomaticChecks` true with a daily interval.
- Remove the interim GitHub check in `UpdateChecker.checkForAppUpdate` at that point; the CLI check stays.

### Why not roll our own in-place updater

Sparkle handles the hard parts we would otherwise re-derive: atomic replace of a running app, EdDSA verification before install, delta updates, rollback on failure, and the standard macOS update UX. The GitHub-only constraint costs nothing since every artifact is a static file.
