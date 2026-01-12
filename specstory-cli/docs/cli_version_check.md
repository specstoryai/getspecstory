# CLI Version Check

## Overview
Add version checking to the SpecStory CLI to notify users when updates are available.

## Status: ✅ COMPLETED

## Implementation Summary

### ✅ Version Check
- On CLI startup, checks for newer versions by checking the redirect from:
  `https://github.com/specstoryai/getspecstory/releases/latest`
- Uses HTTP HEAD request (no full GET needed)
- Extracts version from the final URL after 302 redirect using regex: `/releases/tag/v?(.+)$`
- Compares with current CLI version
- Displays update notification if newer version exists
- Skips version check entirely if current version is "dev" or --no-version-check flag is set

### ✅ User Notification
- Shows simple, non-intrusive message when update available:
  ```
  A new version of SpecStory CLI is available!
  Visit https://get.specstory.com/claude-code for update instructions.
  ```
- Message appears after version check completes
- Runs synchronously (blocking) during CLI startup

### ✅ Configuration
- Added `--no-version-check` flag to skip version checking
- Flag is respected for all commands
- No persistent state/caching needed - check on every run unless flag present

## Technical Implementation Details

### ✅ HTTP Client Configuration
- Uses 2.5-second timeout to prevent delays
- Custom redirect handler to capture redirect URL without following it
- Proper error handling with silent fallback

### ✅ Version Parsing
- Regex pattern matches `/releases/tag/v1.2.3` or `/releases/tag/1.2.3`
- Simple string comparison (adequate for current needs)
- Skips version check entirely for "dev" version (no HTTP request made)

### ✅ Performance Considerations
- Runs synchronously during startup (blocking)
- Includes panic recovery for robustness
- 2.5-second HTTP timeout ensures minimal delays
- No artificial delays added

### ✅ Integration
- Added to main.go directly (no separate module needed)
- Integrated with existing flag system
- Follows existing logging patterns
- Respects existing `--silent` flag