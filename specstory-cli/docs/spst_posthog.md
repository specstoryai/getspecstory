# PostHog Analytics Integration for SpecStory CLI

## Overview

This workplan outlines the integration of PostHog analytics into the SpecStory CLI using the official PostHog Go library. The integration will track key usage events to help understand user behavior and improve the product.

## Events to Track

| Event Name | Event Key | Description | Status |
|------------|-----------|-------------|---------|
| Extension Activated | `ext_activated` | Triggered when extension is loaded in interactive mode | ✅ Implemented |
| Autosave Success | `ext_autosave_success` | Triggered when a new session is autosaved for the first time | ✅ Implemented |
| Autosave Error | `ext_autosave_error` | Triggered when autosave fails for a new session | ✅ Implemented |
| Sync Markdown Success | `ext_sync_markdown_success` | Triggered when --sync-markdown command runs successfully | ✅ Implemented |
| Sync Markdown Error | `ext_sync_markdown_error` | Triggered when --sync-markdown command fails | ✅ Implemented |

## Common Properties
  - `cli_command` - Full command line used ✅
  - `$device_id` - Shared analytics ID ✅
  - `project_path` - Current working directory ✅
  - `editor_name` - Always "Claude Code" for this CLI ✅
  - `editor_type` - Always "claude code" for this CLI ✅
  - `extension_version` - Version of this CLI (same as `specstory --version`) ✅
  - `os_arch` - System architecture (e.g. arm64) ✅
  - `os_name` - Operating system name (e.g. Darwin) ✅
  - `os_platform` - Operating system platform (e.g. darwin) ✅
  - `os_version` - Operating system version (e.g. 24.5.0) ✅
  - `claude_code_version` - Version of Claude Code being used ❌
    - Not implemented because it would require a claude version check which would create a delay for the user

## Implementation Status

### Completed Features
- ✅ PostHog Go library integrated
- ✅ Analytics package created with event tracking
- ✅ Shared analytics ID implementation (macOS only)
- ✅ Automatic inclusion of common properties:
  - `cli_command` - Full command line used
  - `$device_id` - Shared analytics ID
  - `project_path` - Current working directory
- ✅ GeoIP enabled for client-side tracking
- ✅ Event tracking implemented:
  - `ext_activated` - Tracks when Claude Code is launched
  - `ext_autosave_success` - Tracks first-time autosave of new sessions
  - `ext_autosave_error` - Tracks when first-time autosave fails
  - `ext_sync_markdown_success` - Tracks successful markdown sync with session count
  - `ext_sync_markdown_error` - Tracks sync failures with error details
- ✅ `--no-usage-analytics` flag for opting out

## Requirements
- Create a --no-usage-analytics flag that allows the user to run without any analytics tracking ✅
- If specstory cli is installed as part of BearClaude UI, align tracking identifiers for the two so events coming from the CLI and events coming from the UI have the same analytics identifier ✅

## Shared Analytics ID Implementation

### Overview
To align analytics identifiers between BearClaude UI and specstory CLI, both applications will share a common analytics ID stored in a JSON file. This shared ID approach is only used on macOS where BearClaude runs. On other platforms, specstory CLI falls back to generating its own ID.

### Shared File Location (macOS only)
```
~/Library/Application Support/SpecStory/analytics-id.json
```

### JSON File Format
```json
{
  "analytics_id": "uuid-here",
  "created_at": "2025-01-20T10:00:00Z",
  "source": "specstory-cli"  // or "BearClaude"
}
```

### Implementation Strategy
1. Check if running on macOS (runtime.GOOS == "darwin")
2. If on macOS:
   - Check if the shared analytics file exists
   - If exists, read and use the existing analytics_id
   - If not exists:
     - Create the directory structure if needed
     - Generate a new UUID for analytics_id
     - Write the JSON file with metadata
3. If not on macOS or if shared ID fails:
   - Fall back to hostname/username hash (original behavior)
4. Use the determined ID as the PostHog distinct ID

### Benefits
- Single user identity across both tools
- Standard macOS location for application data
- No special permissions required
- Clean separation with vendor-specific directory
## Implementation Plan

### Make analytics fairly self contained
Create separation for the analytics functionality in the go app

### Configuration

The PostHog API key is injected at build time via ldflags in production builds. See `.goreleaser.yml` for the build configuration and `.github/workflows/release.yml` for how the `POSTHOG_API_KEY` secret is passed to GoReleaser.

Development builds have analytics disabled by default (empty API key).



### Documentation

Update README.md with:
- Information about analytics collection
- How to opt-out
- What data is collected
- Privacy policy
