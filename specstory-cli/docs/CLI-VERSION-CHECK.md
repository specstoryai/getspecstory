# CLI version checks and updates

The CLI uses the updater in `pkg/updater`. Startup no longer performs a blocking
HTTP version check or displays the old update banner.

## Automatic updates

The current website curl and PowerShell installers use native paths:
`~/.local/bin/specstory` on macOS/Linux and
`%LOCALAPPDATA%/SpecStory/bin/specstory.exe` on Windows. Eligible `run`, `resume`,
`watch`, and `sync` commands start a detached worker when the six-hour cache is
due. Long-running commands schedule another check after six hours. Running
sessions continue using their executable; the next launch uses the new version.

Homebrew and recognized system packages retain their package-manager update
process. Custom paths, including the repository's legacy `/usr/local/bin`
installer, require an explicit update. Development/prerelease builds and CI do
not auto-update.

## Controls and diagnostics

```zsh
specstory check                 # Local installation and cached update status
specstory update --check        # Query the latest stable release without installing
specstory update                # Update an eligible installation explicitly
specstory update --rollback     # Restore the previous version and pause auto-updates
specstory update --silent       # Perform the update, printing errors only
```

Automatic updates honor `--no-auto-update`, `SPECSTORY_NO_AUTO_UPDATE=1`, and the
existing `--no-version-check` / `version_check.enabled = false` setting. Explicit
update commands remain available. A successful explicit update resumes a rollback
pause. `--print-stdout` commands do not start background updates.

## Release and recovery contract

Both explicit updates and workers use the shared three-minute `updater.Timeout`.
The updater follows the official GitHub latest-release redirect, compares stable
semantic versions, and downloads the version-pinned archive and SHA-256 manifest.
The staged executable must report the expected version within the probe's time
and output limits. These checks trust GitHub release assets over HTTPS; they are
not independent release signatures.

An OS lock serializes updates to each executable. Recovery metadata is saved
before replacement, and each transaction uses a fresh backup filename. If the
final status write fails, the next invocation reconciles the installed binary's
hash with the saved intent, preserving rollback metadata and pause state.

See [automatic update verification](AUTO-UPDATES-VERIFICATION.md) for tests,
library review, failure behavior, and current limitations.
