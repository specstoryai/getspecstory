# Muse Code factory enrollment

Muse supports release detection through `latest-version` and exact installation
through `install <version>`. TOOL-AUDIT enrollment still awaits `list-tools` and
factory credential provisioning. Muse supports `muse exec "<prompt>"`; lack of
a headless mode is not the blocker.

## Release detection and installation

`latest-version` reads Meta's public `muse-stable` channel and preserves the full
`-R` build suffix. `install` fetches the requested version's public release
manifest, selects the macOS/Linux artifact for the current architecture, checks
its size and SHA-256 checksum, and verifies the full version from `--version`
before placing it at `$HOME/.local/bin/muse`.

Neither operation needs credentials. The installer uses the same release
artifacts as Meta's [launcher](https://api.meta.ai/muse-launcher.sh), but installs
the binary directly so the launcher cannot auto-update it during an audit.
An unavailable historical release or any verification failure exits non-zero.

## Requirements for tool-audit enrollment

- **Credentials:** the factory's `scripts/tool-audit-enumerate.sh` runs with
  `env -i` and a fresh `$HOME`. It currently provisions no Muse credentials.
  Muse uses an OAuth credential file (`$HOME/.config/muse/auth.json`, with XDG
  and `MUSE_AUTH_PATH` overrides in the launcher) and device login. Establish
  factory-owned credential provisioning and refresh before enrolling; do not
  depend on the developer's login or an interactive device flow in CI.
- **Enumeration:** verify `muse exec` in that isolated environment, select a
  harness-declared inventory if available or the factory's self-report prompt
  including deferred tools, and fail non-zero on agent/auth errors or missing
  or empty output. Take the two independent readings required by TOOL-AUDIT.

Add executable `list-tools` once that path has been verified. The presence of
both `install` and `list-tools` enrolls the provider in TOOL-AUDIT.

## Historical inventory seed

The factory's `tools/musecode` baseline comes from
`specstory-cli-multi-agent/muse-code/2026-08-16/tools.txt`: 29 names reported by
Muse Code `0.1.0-R708.1`. Sort the names while preserving the original `muse.`
prefix. This is a historical self-report for the first comparison, not a claim
about the current tool surface. TOOL-AUDIT's judge handles normalization against
new readings. Seeding this file does not enroll the provider in tool audits.
