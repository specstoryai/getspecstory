# Cloud Sync HEAD Request Optimization

## Overview

Add HEAD request checking before pushing sessions to the Cloud server to avoid unnecessary uploads. This will check if a session exists and compare markdown sizes to determine if an upload is needed.

## Requirements

1. Use HEAD requests to check session status on server before uploading
2. Check two things from HEAD response:
   - 404 vs 200 response code (session doesn't exist vs exists)
   - X-Markdown-Size header when 200 (compare with local markdown size)
3. Upload decision logic:
   - Upload if server returns 404 (session doesn't exist)
   - Upload if server returns 200 AND local markdown size > server's X-Markdown-Size
   - Skip if server has same or larger markdown size
4. Always check cloud even when local file hasn't changed (catch cloud-missing sessions)
5. Add comprehensive debug logging for HEAD requests and responses
6. Add INFO logging when skipping due to being up-to-date

## Key Changes

### 1. Modify `pkg/cloud/sync.go` - Add HEAD request functionality

Create new function to check if sync is needed:

```go
func shouldSyncSession(sessionID, mdPath, projectID string) (bool, error) {
    // Read local markdown file to get size
    mdContent, err := os.ReadFile(mdPath)
    if err != nil {
        slog.Error("Failed to read markdown file for size check", 
            "sessionId", sessionID, 
            "path", mdPath, 
            "error", err)
        return false, err
    }
    localSize := len(mdContent)
    
    // Make HEAD request
    apiURL := GetAPIBaseURL() + "/api/v1/projects/" + projectID + "/sessions/" + sessionID
    req, err := http.NewRequest("HEAD", apiURL, nil)
    if err != nil {
        slog.Error("Failed to create HEAD request", 
            "sessionId", sessionID, 
            "error", err)
        return false, err
    }
    
    // Add auth headers
    cloudToken := GetCloudToken()
    req.Header.Set("Authorization", "Bearer " + cloudToken)
    req.Header.Set("User-Agent", GetUserAgent())
    
    // Create HTTP client with timeout
    client := &http.Client{
        Timeout: 10 * time.Second, // Shorter timeout for HEAD
    }
    
    slog.Debug("Cloud sync HEAD request", 
        "sessionId", sessionID,
        "url", apiURL,
        "localMarkdownSize", localSize)
    
    // Execute request
    resp, err := client.Do(req)
    if err != nil {
        slog.Debug("HEAD request failed, will attempt sync", 
            "sessionId", sessionID, 
            "error", err)
        return true, nil // Err on side of syncing if HEAD fails
    }
    defer resp.Body.Close()
    
    slog.Debug("Cloud sync HEAD response",
        "sessionId", sessionID,
        "status", resp.StatusCode,
        "headers", resp.Header)
    
    // Check response
    if resp.StatusCode == 404 {
        slog.Debug("Session not found on server, will sync", 
            "sessionId", sessionID)
        return true, nil
    }
    
    if resp.StatusCode == 200 {
        serverSizeStr := resp.Header.Get("X-Markdown-Size")
        if serverSizeStr == "" {
            slog.Debug("No X-Markdown-Size header, will sync", 
                "sessionId", sessionID)
            return true, nil
        }
        
        serverSize, err := strconv.Atoi(serverSizeStr)
        if err != nil {
            slog.Debug("Invalid X-Markdown-Size header, will sync", 
                "sessionId", sessionID,
                "headerValue", serverSizeStr,
                "error", err)
            return true, nil
        }
        
        if localSize > serverSize {
            slog.Debug("Local markdown larger than server, will sync", 
                "sessionId", sessionID,
                "localSize", localSize,
                "serverSize", serverSize)
            return true, nil
        }
        
        slog.Info("Session already up-to-date on server, skipping sync", 
            "sessionId", sessionID,
            "localSize", localSize,
            "serverSize", serverSize)
        return false, nil
    }
    
    // For any other status codes, err on side of syncing
    slog.Debug("Unexpected HEAD response status, will sync",
        "sessionId", sessionID,
        "status", resp.StatusCode)
    return true, nil
}
```

Update `SyncSessionToCloud()` to use the HEAD check:

```go
// In the goroutine, after reading files but before creating APIRequest:

// Determine project ID first (existing code)
projectID := ""
if projectData != nil && projectData.GitID != "" {
    projectID = projectData.GitID
} else if projectData != nil && projectData.WorkspaceID != "" {
    projectID = projectData.WorkspaceID
} else {
    dir := filepath.Dir(jsonlPath)
    projectID = filepath.Base(dir)
}

// Check if sync is needed
shouldSync, err := shouldSyncSession(sessionID, mdPath, projectID)
if err != nil {
    slog.Error("Failed to check if sync needed", 
        "sessionId", sessionID, 
        "error", err)
    // Continue with sync attempt anyway
} else if !shouldSync {
    slog.Info("Skipping cloud sync, already up-to-date",
        "sessionId", sessionID)
    return
}

// Continue with existing sync logic...
```

### 2. Modify `pkg/cli/markdown.go` - Always trigger cloud sync check

#### In `WriteMarkdownFileForSession()` (around line 1008):

```go
// Always trigger cloud sync regardless of whether file was updated
// The cloud module will check via HEAD if sync is actually needed
if len(session.Records) > 0 && session.Records[0].File != "" {
    jsonlPath := session.Records[0].File
    cloud.SyncSessionToCloud(session.SessionUuid, fileFullPath, jsonlPath)
} else {
    slog.Warn("WriteMarkdownFileForSession: No JSONL file path found, skipping cloud sync",
        "sessionId", session.SessionUuid)
}
```

#### In `WriteMarkdownFiles()` (around line 1122):

```go
// Always trigger cloud sync check, even if file wasn't written
// The cloud module will determine via HEAD if upload is needed
if len(session.Records) > 0 && session.Records[0].File != "" {
    jsonlPath := session.Records[0].File
    
    // Need to ensure we have the correct file path even when skipWrite is true
    if skipWrite {
        // File wasn't written but exists, use existing path
        cloud.SyncSessionToCloud(sessionId, fileFullPath, jsonlPath)
    } else if err == nil {
        // File was written successfully
        cloud.SyncSessionToCloud(sessionId, fileFullPath, jsonlPath)
    }
} else {
    slog.Warn("WriteMarkdownFiles: No JSONL file path found, skipping cloud sync",
        "sessionId", sessionId)
}
```

## Benefits

1. **Reduced Network Traffic**: Avoids uploading sessions that are already current
2. **Better Performance**: HEAD requests are much lighter than PUT with full payload  
3. **Catches Missing Sessions**: Will upload sessions that exist locally but not in cloud
4. **Handles Partial Uploads**: Will complete uploads where cloud has smaller/incomplete version
5. **Resilient Design**: Falls back to syncing if HEAD request fails or returns unexpected results

## Testing Considerations

1. Test with sessions that don't exist on server (404 response) - should sync
2. Test with sessions that exist with same size - should skip
3. Test with sessions that exist with smaller size - should sync
4. Test with sessions that exist with larger size - should skip (append-only assumption)
5. Test error handling for network failures - should attempt sync
6. Test missing or invalid X-Markdown-Size header - should attempt sync
7. Verify debug and info logging works correctly
8. Test that cloud sync is checked even when local file hasn't changed

## Implementation Notes

- Import `strconv` package in `sync.go` for `Atoi` function
- Ensure all error paths are handled gracefully with appropriate logging
- HEAD request timeout should be shorter than PUT timeout (10s vs 30s)
- Always err on the side of syncing when uncertain (network errors, invalid headers, etc.)