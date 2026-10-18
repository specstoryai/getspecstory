# CLI automatic update verification

## Behavior and ownership

Native installer paths check in a detached worker during run, resume, watch, and
sync. The worker uses a per-installation OS lock and a six-hour status cache. It
has a three-minute deadline, no terminal handles, and no analytics or provider
initialization. A long-running command schedules another check after six hours.
Running sessions retain their executable; a subsequent launch uses the update.

Homebrew, recognized system package directories, custom paths, development builds,
CI, opt-out flags, and rollback pauses do not auto-update. Explicit custom-path
updates are allowed; package-manager paths direct the user to their package manager.
Native paths refer to the current website installers: `~/.local/bin/specstory` and
`%LOCALAPPDATA%/SpecStory/bin/specstory.exe`. The repository's legacy `install.sh`
defaults to `/usr/local/bin` and remains manual; recognizing any binary there as
native would incorrectly take ownership of unrelated installations.
A read-only version check does not require a writable binary directory, download
an archive, or defer the next automatic installation.

The archive and manifest are pinned to the version returned by the official
GitHub latest-release redirect. Only supported release asset names are requested.
Download redirects are restricted to GitHub HTTPS hosts. Downloads, manifests,
expanded archives, and binary probes are bounded. Extraction reads only the exact
root executable and never writes archive paths or links. SHA-256 and an actual
version probe must pass before replacement. A content comparison detects external
changes made during the download. The OS lock coordinates SpecStory updaters;
uncooperative installers can still race the final comparison and rename, so users
should not run another installer concurrently. Rollback validates the saved binary
hash and version before use.
The probe retains at most 4 KiB of combined stdout/stderr and cancels the process
as soon as it exceeds that limit. `update --silent` suppresses successful update,
check, and rollback messages while preserving errors and performing the operation.

The release contract is explicitly configured in `.goreleaser.yml` as
`SpecStoryCLI_<version>_checksums.txt`. This matches both GoReleaser's documented
[default checksum filename](https://goreleaser.com/customization/package/checksum/)
and the actual [v2.11.0 manifest](https://github.com/specstoryai/getspecstory/releases/download/v2.11.0/SpecStoryCLI_2.11.0_checksums.txt).
The live update test below consumed that versioned asset successfully.

## Go library review

The replacement code uses `github.com/creativeprojects/go-selfupdate/update` from
v1.6.0 (MIT), pinned in go.mod/go.sum. The subpackage keeps the compiled updater
independent of the library's GitLab/Gitea integrations. Reviewed source:
[replacement](https://github.com/creativeprojects/go-selfupdate/blob/v1.6.0/update/apply.go)
and [options](https://github.com/creativeprojects/go-selfupdate/blob/v1.6.0/update/options.go).

The library stages the candidate, saves the old executable, and restores it if
replacement returns an error. The OS lock covers the transaction and backup.
Each transaction reserves a fresh `.specstory.previous-*` backup (or a prefix
matching the executable's filename), so the library cannot delete the preceding
rollback copy before a successful rotation. Old private backups are pruned only
after the new status is saved; Windows-locked files are retried on a later update.

Before replacement, the updater saves an intent containing the target hash,
backup filename/hash/version, and resulting pause state. If the final status write
fails, the next invocation reconciles the current executable hash with this intent.
An unresolved intent prevents background retries until an explicit update. This
preserves rollback information and a rollback pause across post-swap write failures.
Replacement failures with writable status remain retryable on the next launch.
Both worker and explicit commands use the shared `updater.Timeout` constant.

This remains a two-rename transaction, not a guarantee against a power loss or
forced termination between renames. If the executable path is missing, use the
backup named in the status intent/error or rerun the installer to recover.

Module verification is Go checksum verification, and archive verification trusts
the GitHub release manifest over HTTPS. Neither is a formal certification or an
independent release signature. Current releases do not publish a signing key
verified by this implementation.

## Local verification

- Go 1.27.1; full `go test -timeout 5m ./...` and golangci-lint v2.13.2 passed.
- Updater tests passed under the race detector. They cover all six asset targets,
  checksum/manifest rejection, unsafe or missing archive entries, malformed tags,
  foreign redirects, cancellation, package ownership, symlinks, cache and opt-outs,
  concurrent locks, external replacement, and rollback integrity/pause/resumption.
- Native macOS integration built real old/new executables, updated while the old
  process remained running, launched the new version, and rolled back successfully.
- A separate process test verified that a detached worker finishes after its
  launching process exits. Probe failure cleans up its staged executable.
- A native executable fixture emits 3 KiB to each output stream, then sleeps.
  Verification rejects its combined output promptly, leaves the installed binary
  unchanged, and removes the staged probe. A buffer test also covers the exact cap
  and repeated writes after cancellation without retaining additional bytes.
- Command tests exercise inherited `--silent`, a configured default, and an
  explicit `--silent=false` override across update, check, rollback, and already
  current results. Operations still run, and failures remain visible on stderr.
- `goreleaser check` validates the explicit versioned manifest configuration.
- Fault-injection tests cover failure to save the initial intent, all final status
  writes failing after update/rollback, and target rotation failing after the
  library removes its reserved backup path. Existing rollback copies survive,
  rollback remains usable, and a recovered rollback stays paused.
- Go's `testing/synctest` clock exercises the actual six-hour scheduler, successful
  and failed launches, and cancellation. Startup tests cover opt-outs, CI,
  development/manual installs, inspection failure, cached checks, and pause state.
- Read-only checks work for development/prerelease builds without writing status.
- An isolated full CLI invocation fetched the real GitHub v2.11.0 release and
  replaced a development binary labeled 2.10.0. Both the newly installed 2.11.0
  executable and the saved previous executable ran with the expected versions.
  Temporary HOME and install directories were removed afterward.
- `go mod verify`: all modules verified. govulncheck v1.8.0: no vulnerabilities in
  the updater. The full scan reports no reachable vulnerabilities, with one
  advisory in an imported package and three in required modules that are not
  reached. It initially found GO-2026-5320 in existing Goldmark v1.7.13; this branch
  upgrades to the reported fixed version v1.7.17 and the renderer tests pass.

CI runs the full suites on Linux and Windows, updater race checks on Linux and
macOS, dependency verification/scanning, and cross-compiles the remaining release
targets. Fixture tests do not install into a user's actual CLI location or call
the release service.

## Existing RunStory findings

The original checkout had two open findings: f-6362b1910e6e (600 terminal escapes
in piped help) and f-94f42f018823 (roughly four seconds of PTY help overhead).
Both reproduced independently on the starting implementation. The logo bypassed
the color-aware writer, and Fang's help setup queried terminal background colors.

This branch routes the logo through the color-aware writer and uses Cobra's
built-in help renderer. It keeps the hidden man command, version format, and
quiet handling of an agent's exit status. Error styling uses a fixed palette
without a terminal query. Help is now plain, and remains usable in pipes.
After the change, both `help` and `--help` emitted zero escapes into pipes, with
and without NO_COLOR; PTY --help took 33–35 ms with zero escape sequences. A
regression test covers redirected help output.

`runstory verify --only-failed` could not create its isolated sandbox because
E2B_API_KEY was not available. No repair script ran locally, and no original
finding was acknowledged as gone on the unmodified dev branch. The fixes and
independent verification are included in this PR for the next RunStory run.
