# PostHog Analytics

This document describes the analytics implementation in SpecStory CLI.

## Overview

SpecStory CLI uses PostHog for anonymous usage analytics. Analytics help us understand how the tool is used and improve the product. All tracking is optional and can be disabled.

## Opting Out

To disable analytics, use the `--no-usage-analytics` flag:

```zsh
specstory run --no-usage-analytics
specstory watch --no-usage-analytics
specstory sync --no-usage-analytics
```

## Events Tracked

| Event Key                      | Command      | Description                               |
|--------------------------------|--------------|-------------------------------------------|
| `ext_activated`                | `run`        | Triggered when interactive mode starts    |
| `ext_watch_activated`          | `watch`      | Triggered when watch mode starts          |
| `ext_autosave_new`             | `run, watch` | New markdown file created during autosave |
| `ext_autosave_success`         | `run, watch` | Markdown file updated during autosave     |
| `ext_autosave_error`           | `run, watch` | Error writing markdown during autosave    |
| `ext_sync_markdown_new`        | `sync`       | New markdown file created during sync     |
| `ext_sync_markdown_success`    | `sync`       | Markdown file updated during sync         |
| `ext_sync_markdown_error`      | `sync`       | Error writing markdown during sync        |
| `ext_cloudsync_complete`       | all          | Cloud sync completed with statistics      |
| `ext_login_attempted`          | `login`      | User started the login flow               |
| `ext_login_cancelled`          | `login`      | User cancelled the login flow             |
| `ext_login_success`            | `login`      | Successful login to SpecStory Cloud       |
| `ext_login_failed`             | `login`      | Failed login attempt                      |
| `ext_logout`                   | `logout`     | User logged out from SpecStory Cloud      |
| `ext_project_identity_created` | all          | New project identity created              |
| `ext_check_install_success`    | `check`      | Successful agent installation check       |
| `ext_check_install_failed`     | `check`      | Failed agent installation check           |
| `ext_version_command`          | `version`    | User checked the CLI version              |
| `ext_help_command`             | `help`       | User viewed help                          |

## Common Properties

Every event automatically includes these properties:

| Property            | Description                                          |
|---------------------|------------------------------------------------------|
| `cli_command`       | Full command line used                               |
| `$device_id`        | Shared analytics ID (see below)                      |
| `project_path`      | Current working directory                            |
| `agent_provider`    | Array of agent provider names (e.g., ["Claude Code"])|
| `editor_name`       | Always "SpecStory CLI"                               |
| `editor_type`       | Always "CLI"                                         |
| `extension_version` | CLI version                                          |
| `os_arch`           | System architecture (e.g., arm64)                    |
| `os_name`           | OS name from uname (e.g., Darwin)                    |
| `os_platform`       | OS platform (e.g., darwin)                           |
| `os_version`        | OS version                                           |

## Shared Analytics ID

On macOS, SpecStory CLI shares an analytics ID with other SpecStory applications (like BearClaude) to provide a unified view of usage across tools.

**Location:** `~/Library/Application Support/SpecStory/analytics-id.json`

**Format:**

```json
{
  "analytics_id": "uuid-here",
  "created_at": "2025-01-20T10:00:00Z",
  "source": "specstory-cli"
}
```

On non-macOS systems or if the shared ID cannot be read, a fallback ID is generated from a hash of the hostname and username.

## Configuration

The PostHog API key is injected at build time via ldflags. Development builds have analytics disabled (empty API key).

See `.goreleaser.yml` for build configuration and `.github/workflows/release.yml` for how the `POSTHOG_API_KEY` secret is passed during release builds.

## Implementation

The analytics code is in `pkg/analytics/`:

- `client.go` - PostHog client initialization and configuration
- `events.go` - Event constants and the `TrackEvent` function
- `shared_id.go` - Shared analytics ID management
