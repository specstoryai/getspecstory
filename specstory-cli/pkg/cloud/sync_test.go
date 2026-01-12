package cloud

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestShouldReplaceCategory tests the precedence logic for category replacement
func TestShouldReplaceCategory(t *testing.T) {
	tests := []struct {
		name        string
		oldCategory syncCategory
		newCategory syncCategory
		want        bool
	}{
		// Created never gets replaced
		{"created never replaced by updated", categoryCreated, categoryUpdated, false},
		{"created never replaced by errored", categoryCreated, categoryErrored, false},
		{"created never replaced by skipped", categoryCreated, categorySkipped, false},
		{"created never replaced by created", categoryCreated, categoryCreated, false},

		// Updated replaces skipped and errored
		{"updated replaces skipped", categorySkipped, categoryUpdated, true},
		{"updated replaces errored", categoryErrored, categoryUpdated, true},
		{"updated doesn't replace created", categoryCreated, categoryUpdated, false},
		{"updated doesn't replace updated", categoryUpdated, categoryUpdated, false},

		// Errored replaces only skipped
		{"errored replaces skipped", categorySkipped, categoryErrored, true},
		{"errored doesn't replace updated", categoryUpdated, categoryErrored, false},
		{"errored doesn't replace created", categoryCreated, categoryErrored, false},
		{"errored doesn't replace errored", categoryErrored, categoryErrored, false},

		// Created replaces everything
		{"created replaces skipped", categorySkipped, categoryCreated, true},
		{"created replaces errored", categoryErrored, categoryCreated, true},
		{"created replaces updated", categoryUpdated, categoryCreated, true},

		// Skipped doesn't replace anything
		{"skipped doesn't replace skipped", categorySkipped, categorySkipped, false},
		{"skipped doesn't replace errored", categoryErrored, categorySkipped, false},
		{"skipped doesn't replace updated", categoryUpdated, categorySkipped, false},
		{"skipped doesn't replace created", categoryCreated, categorySkipped, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldReplaceCategory(tt.oldCategory, tt.newCategory)
			if got != tt.want {
				t.Errorf("shouldReplaceCategory(%s, %s) = %v, want %v",
					tt.oldCategory, tt.newCategory, got, tt.want)
			}
		})
	}
}

// TestTrackSessionInCategory_Sequential tests sequential category tracking
func TestTrackSessionInCategory_Sequential(t *testing.T) {
	tests := []struct {
		name       string
		sequence   []syncCategory
		wantFinal  syncCategory
		wantCounts map[syncCategory]int32
	}{
		{
			name:      "single skipped session",
			sequence:  []syncCategory{categorySkipped},
			wantFinal: categorySkipped,
			wantCounts: map[syncCategory]int32{
				categorySkipped: 1,
				categoryErrored: 0,
				categoryUpdated: 0,
				categoryCreated: 0,
			},
		},
		{
			name:      "skipped then updated",
			sequence:  []syncCategory{categorySkipped, categoryUpdated},
			wantFinal: categoryUpdated,
			wantCounts: map[syncCategory]int32{
				categorySkipped: 0,
				categoryErrored: 0,
				categoryUpdated: 1,
				categoryCreated: 0,
			},
		},
		{
			name:      "skipped then errored",
			sequence:  []syncCategory{categorySkipped, categoryErrored},
			wantFinal: categoryErrored,
			wantCounts: map[syncCategory]int32{
				categorySkipped: 0,
				categoryErrored: 1,
				categoryUpdated: 0,
				categoryCreated: 0,
			},
		},
		{
			name:      "skipped then errored then updated",
			sequence:  []syncCategory{categorySkipped, categoryErrored, categoryUpdated},
			wantFinal: categoryUpdated,
			wantCounts: map[syncCategory]int32{
				categorySkipped: 0,
				categoryErrored: 0,
				categoryUpdated: 1,
				categoryCreated: 0,
			},
		},
		{
			name:      "full progression to created",
			sequence:  []syncCategory{categorySkipped, categoryErrored, categoryUpdated, categoryCreated},
			wantFinal: categoryCreated,
			wantCounts: map[syncCategory]int32{
				categorySkipped: 0,
				categoryErrored: 0,
				categoryUpdated: 0,
				categoryCreated: 1,
			},
		},
		{
			name:      "created doesn't downgrade",
			sequence:  []syncCategory{categoryCreated, categoryUpdated, categoryErrored, categorySkipped},
			wantFinal: categoryCreated,
			wantCounts: map[syncCategory]int32{
				categorySkipped: 0,
				categoryErrored: 0,
				categoryUpdated: 0,
				categoryCreated: 1,
			},
		},
		{
			name:      "updated doesn't downgrade to errored",
			sequence:  []syncCategory{categoryUpdated, categoryErrored},
			wantFinal: categoryUpdated,
			wantCounts: map[syncCategory]int32{
				categorySkipped: 0,
				categoryErrored: 0,
				categoryUpdated: 1,
				categoryCreated: 0,
			},
		},
		{
			name:      "errored doesn't downgrade to skipped",
			sequence:  []syncCategory{categoryErrored, categorySkipped},
			wantFinal: categoryErrored,
			wantCounts: map[syncCategory]int32{
				categorySkipped: 0,
				categoryErrored: 1,
				categoryUpdated: 0,
				categoryCreated: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := &CloudSyncStats{}
			sessionID := "test-session"

			// Apply sequence of categories
			for _, category := range tt.sequence {
				stats.trackSessionInCategory(sessionID, category)
			}

			// Check final category in map
			finalCategoryRaw, ok := stats.countedSessions.Load(sessionID)
			if !ok {
				t.Errorf("session not found in countedSessions map")
			} else {
				finalCategory := finalCategoryRaw.(syncCategory)
				if finalCategory != tt.wantFinal {
					t.Errorf("final category = %s, want %s", finalCategory, tt.wantFinal)
				}
			}

			// Check counter values
			gotSkipped := atomic.LoadInt32(&stats.SessionsSkipped)
			gotErrored := atomic.LoadInt32(&stats.SessionsErrored)
			gotUpdated := atomic.LoadInt32(&stats.SessionsUpdated)
			gotCreated := atomic.LoadInt32(&stats.SessionsCreated)

			if gotSkipped != tt.wantCounts[categorySkipped] {
				t.Errorf("SessionsSkipped = %d, want %d", gotSkipped, tt.wantCounts[categorySkipped])
			}
			if gotErrored != tt.wantCounts[categoryErrored] {
				t.Errorf("SessionsErrored = %d, want %d", gotErrored, tt.wantCounts[categoryErrored])
			}
			if gotUpdated != tt.wantCounts[categoryUpdated] {
				t.Errorf("SessionsUpdated = %d, want %d", gotUpdated, tt.wantCounts[categoryUpdated])
			}
			if gotCreated != tt.wantCounts[categoryCreated] {
				t.Errorf("SessionsCreated = %d, want %d", gotCreated, tt.wantCounts[categoryCreated])
			}
		})
	}
}

// TestTrackSessionInCategory_MultipleSessions tests tracking multiple different sessions
func TestTrackSessionInCategory_MultipleSessions(t *testing.T) {
	stats := &CloudSyncStats{}

	// Track different sessions in different categories
	stats.trackSessionInCategory("session1", categorySkipped)
	stats.trackSessionInCategory("session2", categoryCreated)
	stats.trackSessionInCategory("session3", categoryUpdated)
	stats.trackSessionInCategory("session4", categoryErrored)

	// Transition session1 from skipped to updated
	stats.trackSessionInCategory("session1", categoryUpdated)

	// Transition session4 from errored to updated
	stats.trackSessionInCategory("session4", categoryUpdated)

	// Expected counts: 0 skipped, 0 errored, 3 updated (session1, session3, session4), 1 created (session2)
	wantSkipped := int32(0)
	wantErrored := int32(0)
	wantUpdated := int32(3)
	wantCreated := int32(1)

	gotSkipped := atomic.LoadInt32(&stats.SessionsSkipped)
	gotErrored := atomic.LoadInt32(&stats.SessionsErrored)
	gotUpdated := atomic.LoadInt32(&stats.SessionsUpdated)
	gotCreated := atomic.LoadInt32(&stats.SessionsCreated)

	if gotSkipped != wantSkipped {
		t.Errorf("SessionsSkipped = %d, want %d", gotSkipped, wantSkipped)
	}
	if gotErrored != wantErrored {
		t.Errorf("SessionsErrored = %d, want %d", gotErrored, wantErrored)
	}
	if gotUpdated != wantUpdated {
		t.Errorf("SessionsUpdated = %d, want %d", gotUpdated, wantUpdated)
	}
	if gotCreated != wantCreated {
		t.Errorf("SessionsCreated = %d, want %d", gotCreated, wantCreated)
	}
}

// TestTrackSessionInCategory_Concurrent tests concurrent updates to the same session
func TestTrackSessionInCategory_Concurrent(t *testing.T) {
	tests := []struct {
		name          string
		operations    []syncCategory
		expectedFinal syncCategory
	}{
		{
			name:          "concurrent skipped and updated",
			operations:    []syncCategory{categorySkipped, categoryUpdated},
			expectedFinal: categoryUpdated, // Updated should win
		},
		{
			name:          "concurrent all categories",
			operations:    []syncCategory{categorySkipped, categoryErrored, categoryUpdated, categoryCreated},
			expectedFinal: categoryCreated, // Created has highest precedence
		},
		{
			name:          "concurrent updated and errored",
			operations:    []syncCategory{categoryUpdated, categoryErrored},
			expectedFinal: categoryUpdated, // Updated has higher precedence than errored
		},
		{
			name:          "many concurrent skipped",
			operations:    []syncCategory{categorySkipped, categorySkipped, categorySkipped, categorySkipped},
			expectedFinal: categorySkipped,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := &CloudSyncStats{}
			sessionID := "concurrent-session"

			// Launch goroutines for concurrent operations
			var wg sync.WaitGroup
			for _, category := range tt.operations {
				wg.Add(1)
				cat := category // Capture for goroutine
				go func() {
					defer wg.Done()
					stats.trackSessionInCategory(sessionID, cat)
				}()
			}

			// Wait for all operations to complete
			wg.Wait()

			// Check final category
			finalCategoryRaw, ok := stats.countedSessions.Load(sessionID)
			if !ok {
				t.Errorf("session not found in countedSessions map")
			} else {
				finalCategory := finalCategoryRaw.(syncCategory)
				if finalCategory != tt.expectedFinal {
					t.Errorf("final category = %s, want %s", finalCategory, tt.expectedFinal)
				}
			}

			// Verify counters: exactly one session should be counted in exactly one category
			gotSkipped := atomic.LoadInt32(&stats.SessionsSkipped)
			gotErrored := atomic.LoadInt32(&stats.SessionsErrored)
			gotUpdated := atomic.LoadInt32(&stats.SessionsUpdated)
			gotCreated := atomic.LoadInt32(&stats.SessionsCreated)

			total := gotSkipped + gotErrored + gotUpdated + gotCreated
			if total != 1 {
				t.Errorf("total count = %d, want 1 (skipped=%d, errored=%d, updated=%d, created=%d)",
					total, gotSkipped, gotErrored, gotUpdated, gotCreated)
			}

			// Verify the count is in the expected category
			switch tt.expectedFinal {
			case categorySkipped:
				if gotSkipped != 1 {
					t.Errorf("SessionsSkipped = %d, want 1", gotSkipped)
				}
			case categoryErrored:
				if gotErrored != 1 {
					t.Errorf("SessionsErrored = %d, want 1", gotErrored)
				}
			case categoryUpdated:
				if gotUpdated != 1 {
					t.Errorf("SessionsUpdated = %d, want 1", gotUpdated)
				}
			case categoryCreated:
				if gotCreated != 1 {
					t.Errorf("SessionsCreated = %d, want 1", gotCreated)
				}
			}
		})
	}
}

// TestTrackSessionInCategory_ManyConcurrentSessions tests many sessions being tracked concurrently
func TestTrackSessionInCategory_ManyConcurrentSessions(t *testing.T) {
	stats := &CloudSyncStats{}
	numSessions := 100
	var wg sync.WaitGroup

	// Track 100 sessions concurrently
	// 25 skipped, 25 errored, 25 updated, 25 created
	for i := 0; i < numSessions; i++ {
		wg.Add(1)
		sessionID := i
		go func() {
			defer wg.Done()
			category := categorySkipped
			switch sessionID % 4 {
			case 0:
				category = categorySkipped
			case 1:
				category = categoryErrored
			case 2:
				category = categoryUpdated
			case 3:
				category = categoryCreated
			}
			stats.trackSessionInCategory(string(rune(sessionID)), category)
		}()
	}

	wg.Wait()

	// Check totals
	gotSkipped := atomic.LoadInt32(&stats.SessionsSkipped)
	gotErrored := atomic.LoadInt32(&stats.SessionsErrored)
	gotUpdated := atomic.LoadInt32(&stats.SessionsUpdated)
	gotCreated := atomic.LoadInt32(&stats.SessionsCreated)

	total := gotSkipped + gotErrored + gotUpdated + gotCreated
	if total != int32(numSessions) {
		t.Errorf("total count = %d, want %d", total, numSessions)
	}

	// Each category should have 25 sessions
	wantPerCategory := int32(25)
	if gotSkipped != wantPerCategory {
		t.Errorf("SessionsSkipped = %d, want %d", gotSkipped, wantPerCategory)
	}
	if gotErrored != wantPerCategory {
		t.Errorf("SessionsErrored = %d, want %d", gotErrored, wantPerCategory)
	}
	if gotUpdated != wantPerCategory {
		t.Errorf("SessionsUpdated = %d, want %d", gotUpdated, wantPerCategory)
	}
	if gotCreated != wantPerCategory {
		t.Errorf("SessionsCreated = %d, want %d", gotCreated, wantPerCategory)
	}
}

// TestIncrementDecrement_AllCategories tests that increment/decrement work for all categories
func TestIncrementDecrement_AllCategories(t *testing.T) {
	stats := &CloudSyncStats{}

	// Increment all categories
	stats.incrementCategory(categorySkipped)
	stats.incrementCategory(categoryErrored)
	stats.incrementCategory(categoryUpdated)
	stats.incrementCategory(categoryCreated)

	// Check all are 1
	if got := atomic.LoadInt32(&stats.SessionsSkipped); got != 1 {
		t.Errorf("SessionsSkipped after increment = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&stats.SessionsErrored); got != 1 {
		t.Errorf("SessionsErrored after increment = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&stats.SessionsUpdated); got != 1 {
		t.Errorf("SessionsUpdated after increment = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&stats.SessionsCreated); got != 1 {
		t.Errorf("SessionsCreated after increment = %d, want 1", got)
	}

	// Decrement all categories
	stats.decrementCategory(categorySkipped)
	stats.decrementCategory(categoryErrored)
	stats.decrementCategory(categoryUpdated)
	stats.decrementCategory(categoryCreated)

	// Check all are 0
	if got := atomic.LoadInt32(&stats.SessionsSkipped); got != 0 {
		t.Errorf("SessionsSkipped after decrement = %d, want 0", got)
	}
	if got := atomic.LoadInt32(&stats.SessionsErrored); got != 0 {
		t.Errorf("SessionsErrored after decrement = %d, want 0", got)
	}
	if got := atomic.LoadInt32(&stats.SessionsUpdated); got != 0 {
		t.Errorf("SessionsUpdated after decrement = %d, want 0", got)
	}
	if got := atomic.LoadInt32(&stats.SessionsCreated); got != 0 {
		t.Errorf("SessionsCreated after decrement = %d, want 0", got)
	}
}

// TestRequiresSync_WithBulkSizes tests requiresSync when bulk sizes are preloaded
func TestRequiresSync_WithBulkSizes(t *testing.T) {
	tests := []struct {
		name          string
		sessionID     string
		localContent  string
		bulkSizes     map[string]int // nil means not preloaded
		skipHeadCheck bool
		wantNeedsSync bool
		wantErr       bool
		description   string
	}{
		{
			name:          "skipHeadCheck true always needs sync",
			sessionID:     "session-1",
			localContent:  "local content",
			bulkSizes:     map[string]int{"session-1": 10},
			skipHeadCheck: true,
			wantNeedsSync: true,
			wantErr:       false,
			description:   "When skipHeadCheck is true, should return true immediately without checking bulk sizes",
		},
		{
			name:          "bulk sizes available, session exists, sizes match",
			sessionID:     "session-1",
			localContent:  "1234567890", // 10 bytes
			bulkSizes:     map[string]int{"session-1": 10},
			skipHeadCheck: false,
			wantNeedsSync: false,
			wantErr:       false,
			description:   "When local and server sizes match, no sync needed",
		},
		{
			name:          "bulk sizes available, session exists, local larger",
			sessionID:     "session-1",
			localContent:  "12345678901234567890", // 20 bytes
			bulkSizes:     map[string]int{"session-1": 10},
			skipHeadCheck: false,
			wantNeedsSync: true,
			wantErr:       false,
			description:   "When local size > server size, sync needed",
		},
		{
			name:          "bulk sizes available, session exists, local smaller",
			sessionID:     "session-1",
			localContent:  "12345", // 5 bytes
			bulkSizes:     map[string]int{"session-1": 10},
			skipHeadCheck: false,
			wantNeedsSync: false,
			wantErr:       false,
			description:   "When local size < server size, no sync needed",
		},
		{
			name:          "bulk sizes available, session not in map",
			sessionID:     "session-new",
			localContent:  "new content",
			bulkSizes:     map[string]int{"session-1": 10, "session-2": 20},
			skipHeadCheck: false,
			wantNeedsSync: true,
			wantErr:       false,
			description:   "When session not in bulk sizes map, means it doesn't exist on server - sync needed",
		},
		{
			name:          "bulk sizes available, empty map",
			sessionID:     "session-1",
			localContent:  "content",
			bulkSizes:     map[string]int{},
			skipHeadCheck: false,
			wantNeedsSync: true,
			wantErr:       false,
			description:   "Empty bulk sizes map means session doesn't exist on server",
		},
		{
			name:          "bulk sizes nil - uses HEAD request path",
			sessionID:     "session-1",
			localContent:  "content",
			bulkSizes:     nil,
			skipHeadCheck: false,
			wantNeedsSync: true, // Will attempt HEAD request which will fail in test, defaults to sync
			wantErr:       false,
			description:   "When bulk sizes not preloaded, falls back to HEAD request (which will fail in this test)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Initialize sync manager with bulk sizes
			InitSyncManager(true)
			syncMgr := GetSyncManager()
			if syncMgr == nil {
				t.Fatal("Failed to get sync manager")
			}

			// Set bulk sizes if provided
			syncMgr.bulkSizesMu.Lock()
			syncMgr.bulkSizes = tt.bulkSizes
			syncMgr.bulkSizesMu.Unlock()

			// Call requiresSync
			needsSync, err := syncMgr.requiresSync(
				tt.sessionID,
				"/path/to/session.md",
				tt.localContent,
				"test-project-id",
				tt.skipHeadCheck,
			)

			// Check error
			if (err != nil) != tt.wantErr {
				t.Errorf("requiresSync() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Check result
			if needsSync != tt.wantNeedsSync {
				t.Errorf("requiresSync() = %v, want %v\nDescription: %s\nLocal size: %d, Server size: %v",
					needsSync, tt.wantNeedsSync, tt.description, len(tt.localContent),
					func() interface{} {
						if tt.bulkSizes == nil {
							return "nil (no bulk sizes)"
						}
						if size, ok := tt.bulkSizes[tt.sessionID]; ok {
							return size
						}
						return "not in map"
					}())
			}

			// Cleanup
			syncMgr.bulkSizesMu.Lock()
			syncMgr.bulkSizes = nil
			syncMgr.bulkSizesMu.Unlock()
		})
	}
}

// TestRequiresSync_BulkSizesEdgeCases tests edge cases for bulk sizes
func TestRequiresSync_BulkSizesEdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		localContent  string
		serverSize    int
		wantNeedsSync bool
		description   string
	}{
		{
			name:          "zero length local, zero length server",
			localContent:  "",
			serverSize:    0,
			wantNeedsSync: false,
			description:   "Empty files with matching sizes",
		},
		{
			name:          "zero length local, non-zero server",
			localContent:  "",
			serverSize:    10,
			wantNeedsSync: false,
			description:   "Empty local file, server has content - no sync needed",
		},
		{
			name:          "non-zero local, zero server",
			localContent:  "content",
			serverSize:    0,
			wantNeedsSync: true,
			description:   "Local has content, server empty - sync needed",
		},
		{
			name:          "very large size difference",
			localContent:  string(make([]byte, 1000000)), // 1MB
			serverSize:    100,
			wantNeedsSync: true,
			description:   "Large local file, small server file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			InitSyncManager(true)
			syncMgr := GetSyncManager()
			if syncMgr == nil {
				t.Fatal("Failed to get sync manager")
			}

			// Set bulk sizes with test session
			sessionID := "edge-case-session"
			bulkSizes := map[string]int{sessionID: tt.serverSize}
			syncMgr.bulkSizesMu.Lock()
			syncMgr.bulkSizes = bulkSizes
			syncMgr.bulkSizesMu.Unlock()

			needsSync, err := syncMgr.requiresSync(
				sessionID,
				"/path/to/session.md",
				tt.localContent,
				"test-project-id",
				false,
			)

			if err != nil {
				t.Errorf("requiresSync() unexpected error: %v", err)
			}

			if needsSync != tt.wantNeedsSync {
				t.Errorf("requiresSync() = %v, want %v\nDescription: %s\nLocal size: %d, Server size: %d",
					needsSync, tt.wantNeedsSync, tt.description, len(tt.localContent), tt.serverSize)
			}

			// Cleanup
			syncMgr.bulkSizesMu.Lock()
			syncMgr.bulkSizes = nil
			syncMgr.bulkSizesMu.Unlock()
		})
	}
}

// TestCalculateTimedOut_DefensiveValidation tests that calculateTimedOut handles counter inconsistencies
func TestCalculateTimedOut_DefensiveValidation(t *testing.T) {
	tests := []struct {
		name         string
		attempted    int32
		skipped      int32
		updated      int32
		created      int32
		errored      int32
		wantTimedOut int32
		description  string
	}{
		{
			name:         "normal case: some sessions timed out",
			attempted:    10,
			skipped:      3,
			updated:      2,
			created:      1,
			errored:      2,
			wantTimedOut: 2, // 10 - (3+2+1+2) = 2
			description:  "Normal case where attempted > finished",
		},
		{
			name:         "normal case: no timeouts",
			attempted:    10,
			skipped:      4,
			updated:      3,
			created:      2,
			errored:      1,
			wantTimedOut: 0, // 10 - (4+3+2+1) = 0
			description:  "All sessions accounted for, no timeouts",
		},
		{
			name:         "inconsistency: finished > attempted",
			attempted:    5,
			skipped:      3,
			updated:      2,
			created:      2,
			errored:      1,
			wantTimedOut: 0, // Defensive: should set to 0 instead of negative
			description:  "Bug scenario where counters are inconsistent (finished=8 > attempted=5)",
		},
		{
			name:         "inconsistency: finished much larger than attempted",
			attempted:    1,
			skipped:      10,
			updated:      5,
			created:      5,
			errored:      5,
			wantTimedOut: 0, // Defensive: should set to 0 instead of -24
			description:  "Extreme bug scenario (finished=25 > attempted=1)",
		},
		{
			name:         "edge case: zero attempted",
			attempted:    0,
			skipped:      0,
			updated:      0,
			created:      0,
			errored:      0,
			wantTimedOut: 0,
			description:  "No sessions attempted at all",
		},
		{
			name:         "edge case: all sessions attempted but none finished",
			attempted:    100,
			skipped:      0,
			updated:      0,
			created:      0,
			errored:      0,
			wantTimedOut: 100,
			description:  "All sessions timed out (worst case scenario)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := &CloudSyncStats{
				SessionsAttempted: tt.attempted,
				SessionsSkipped:   tt.skipped,
				SessionsUpdated:   tt.updated,
				SessionsCreated:   tt.created,
				SessionsErrored:   tt.errored,
			}

			// Call calculateTimedOut
			stats.calculateTimedOut()

			// Verify result
			if stats.SessionsTimedOut != tt.wantTimedOut {
				t.Errorf("calculateTimedOut() resulted in SessionsTimedOut = %d, want %d\nDescription: %s\nAttempted: %d, Finished: %d (skipped=%d, updated=%d, created=%d, errored=%d)",
					stats.SessionsTimedOut, tt.wantTimedOut, tt.description,
					tt.attempted, tt.skipped+tt.updated+tt.created+tt.errored,
					tt.skipped, tt.updated, tt.created, tt.errored)
			}

			// Verify it never goes negative
			if stats.SessionsTimedOut < 0 {
				t.Errorf("calculateTimedOut() resulted in negative SessionsTimedOut = %d (should never be negative)\nDescription: %s",
					stats.SessionsTimedOut, tt.description)
			}
		})
	}
}

// TestSessionsAttemptedDeduplication tests that SessionsAttempted only counts each unique session once
// This simulates the behavior in performSync where multiple calls for the same session
// (e.g., due to debouncing in run mode) should only increment the counter once
func TestSessionsAttemptedDeduplication(t *testing.T) {
	tests := []struct {
		name              string
		sessionIDs        []string // Sessions to "attempt" in order
		wantAttempted     int32    // Expected SessionsAttempted count
		wantUniqueTracked int      // Expected number of unique sessions in countedSessions map
	}{
		{
			name:              "single session counted once",
			sessionIDs:        []string{"session-1"},
			wantAttempted:     1,
			wantUniqueTracked: 1,
		},
		{
			name:              "same session multiple times counted once",
			sessionIDs:        []string{"session-1", "session-1", "session-1"},
			wantAttempted:     1,
			wantUniqueTracked: 1,
		},
		{
			name:              "different sessions counted separately",
			sessionIDs:        []string{"session-1", "session-2", "session-3"},
			wantAttempted:     3,
			wantUniqueTracked: 3,
		},
		{
			name:              "mixed duplicate and unique sessions",
			sessionIDs:        []string{"session-1", "session-2", "session-1", "session-3", "session-2"},
			wantAttempted:     3,
			wantUniqueTracked: 3,
		},
		{
			name:              "many duplicates of same session",
			sessionIDs:        []string{"session-1", "session-1", "session-1", "session-1", "session-1", "session-1", "session-1", "session-1", "session-1", "session-1"},
			wantAttempted:     1,
			wantUniqueTracked: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := &CloudSyncStats{}

			// Simulate the SessionsAttempted increment logic from performSync
			// which checks countedSessions.Load() before incrementing
			for _, sessionID := range tt.sessionIDs {
				// This mimics the logic in performSync lines 707-709
				if _, alreadyTracked := stats.countedSessions.Load(sessionID); !alreadyTracked {
					atomic.AddInt32(&stats.SessionsAttempted, 1)
				}

				// Track the session in a category (this stores it in the map)
				// In real code, this happens later in performSync on every code path
				stats.trackSessionInCategory(sessionID, categorySkipped)
			}

			// Verify SessionsAttempted count
			gotAttempted := atomic.LoadInt32(&stats.SessionsAttempted)
			if gotAttempted != tt.wantAttempted {
				t.Errorf("SessionsAttempted = %d, want %d", gotAttempted, tt.wantAttempted)
			}

			// Verify the number of unique sessions tracked in the map
			uniqueCount := 0
			stats.countedSessions.Range(func(key, value interface{}) bool {
				uniqueCount++
				return true // continue iteration
			})
			if uniqueCount != tt.wantUniqueTracked {
				t.Errorf("unique sessions in countedSessions = %d, want %d", uniqueCount, tt.wantUniqueTracked)
			}
		})
	}
}

// TestSessionsAttemptedWithCategoryTransitions tests that SessionsAttempted deduplication works
// correctly even when sessions transition between categories
func TestSessionsAttemptedWithCategoryTransitions(t *testing.T) {
	stats := &CloudSyncStats{}
	sessionID := "test-session"

	// First "attempt" - should increment
	if _, alreadyTracked := stats.countedSessions.Load(sessionID); !alreadyTracked {
		atomic.AddInt32(&stats.SessionsAttempted, 1)
	}
	stats.trackSessionInCategory(sessionID, categorySkipped)

	// Check count after first attempt
	if atomic.LoadInt32(&stats.SessionsAttempted) != 1 {
		t.Errorf("SessionsAttempted after first attempt = %d, want 1", stats.SessionsAttempted)
	}

	// Transition to updated category
	stats.trackSessionInCategory(sessionID, categoryUpdated)

	// Second "attempt" after category transition - should NOT increment
	if _, alreadyTracked := stats.countedSessions.Load(sessionID); !alreadyTracked {
		atomic.AddInt32(&stats.SessionsAttempted, 1)
	}

	// Check count after second attempt - should still be 1
	if atomic.LoadInt32(&stats.SessionsAttempted) != 1 {
		t.Errorf("SessionsAttempted after second attempt = %d, want 1 (should not increment on duplicate)", stats.SessionsAttempted)
	}

	// Verify the session is in updated category
	categoryRaw, ok := stats.countedSessions.Load(sessionID)
	if !ok {
		t.Fatal("session not found in countedSessions")
	}
	if categoryRaw.(syncCategory) != categoryUpdated {
		t.Errorf("session category = %v, want %v", categoryRaw, categoryUpdated)
	}

	// Verify counters
	if atomic.LoadInt32(&stats.SessionsSkipped) != 0 {
		t.Errorf("SessionsSkipped = %d, want 0", stats.SessionsSkipped)
	}
	if atomic.LoadInt32(&stats.SessionsUpdated) != 1 {
		t.Errorf("SessionsUpdated = %d, want 1", stats.SessionsUpdated)
	}
}
