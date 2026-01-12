package cursorcli

import (
	"encoding/json"
	"testing"
)

func TestTopologicalSort(t *testing.T) {
	tests := []struct {
		name          string
		blobs         map[string]*CursorBlob
		expectedOrder []string // Expected blob IDs in order
	}{
		{
			name: "linear chain of references",
			blobs: map[string]*CursorBlob{
				"blob1": {
					RowID:      1,
					ID:         "blob1",
					Data:       json.RawMessage(`{"msg":"first"}`),
					References: []string{},
				},
				"blob2": {
					RowID:      2,
					ID:         "blob2",
					Data:       json.RawMessage(`{"msg":"second"}`),
					References: []string{"blob1"},
				},
				"blob3": {
					RowID:      3,
					ID:         "blob3",
					Data:       json.RawMessage(`{"msg":"third"}`),
					References: []string{"blob2"},
				},
			},
			expectedOrder: []string{"blob1", "blob2", "blob3"},
		},
		{
			name: "branching conversation",
			blobs: map[string]*CursorBlob{
				"root": {
					RowID:      1,
					ID:         "root",
					Data:       json.RawMessage(`{"msg":"root"}`),
					References: []string{},
				},
				"branch1": {
					RowID:      2,
					ID:         "branch1",
					Data:       json.RawMessage(`{"msg":"branch1"}`),
					References: []string{"root"},
				},
				"branch2": {
					RowID:      3,
					ID:         "branch2",
					Data:       json.RawMessage(`{"msg":"branch2"}`),
					References: []string{"root"},
				},
				"end": {
					RowID:      4,
					ID:         "end",
					Data:       json.RawMessage(`{"msg":"end"}`),
					References: []string{"branch1", "branch2"},
				},
			},
			expectedOrder: []string{"root", "branch1", "branch2", "end"},
		},
		{
			name: "orphaned blobs",
			blobs: map[string]*CursorBlob{
				"connected1": {
					RowID:      1,
					ID:         "connected1",
					Data:       json.RawMessage(`{"msg":"connected1"}`),
					References: []string{},
				},
				"connected2": {
					RowID:      5, // Higher rowid to ensure it's picked as end blob
					ID:         "connected2",
					Data:       json.RawMessage(`{"msg":"connected2"}`),
					References: []string{"connected1"},
				},
				"orphan1": {
					RowID:      2,
					ID:         "orphan1",
					Data:       json.RawMessage(`{"msg":"orphan1"}`),
					References: []string{},
				},
				"orphan2": {
					RowID:      3,
					ID:         "orphan2",
					Data:       json.RawMessage(`{"msg":"orphan2"}`),
					References: []string{"orphan1"},
				},
			},
			// Should return the connected chain (orphaned blobs are ignored)
			expectedOrder: []string{"connected1", "connected2"},
		},
		{
			name:          "empty blob map",
			blobs:         map[string]*CursorBlob{},
			expectedOrder: []string{},
		},
		{
			name: "single blob no references",
			blobs: map[string]*CursorBlob{
				"single": {
					RowID:      1,
					ID:         "single",
					Data:       json.RawMessage(`{"msg":"single"}`),
					References: []string{},
				},
			},
			expectedOrder: []string{"single"},
		},
		{
			name: "reference to non-existent blob",
			blobs: map[string]*CursorBlob{
				"blob1": {
					RowID:      1,
					ID:         "blob1",
					Data:       json.RawMessage(`{"msg":"first"}`),
					References: []string{"nonexistent"},
				},
				"blob2": {
					RowID:      2,
					ID:         "blob2",
					Data:       json.RawMessage(`{"msg":"second"}`),
					References: []string{"blob1"},
				},
			},
			expectedOrder: []string{"blob1", "blob2"},
		},
		{
			name: "complex DAG with multiple paths",
			blobs: map[string]*CursorBlob{
				"a": {
					RowID:      1,
					ID:         "a",
					Data:       json.RawMessage(`{"msg":"a"}`),
					References: []string{},
				},
				"b": {
					RowID:      2,
					ID:         "b",
					Data:       json.RawMessage(`{"msg":"b"}`),
					References: []string{"a"},
				},
				"c": {
					RowID:      3,
					ID:         "c",
					Data:       json.RawMessage(`{"msg":"c"}`),
					References: []string{"a"},
				},
				"d": {
					RowID:      4,
					ID:         "d",
					Data:       json.RawMessage(`{"msg":"d"}`),
					References: []string{"b", "c"},
				},
				"e": {
					RowID:      5,
					ID:         "e",
					Data:       json.RawMessage(`{"msg":"e"}`),
					References: []string{"c"},
				},
				"f": {
					RowID:      6,
					ID:         "f",
					Data:       json.RawMessage(`{"msg":"f"}`),
					References: []string{"d", "e"},
				},
			},
			expectedOrder: []string{"a", "b", "c", "d", "e", "f"},
		},
		{
			name: "tie-breaking by rowid",
			blobs: map[string]*CursorBlob{
				"older": {
					RowID:      1,
					ID:         "older",
					Data:       json.RawMessage(`{"msg":"older"}`),
					References: []string{"ref1"},
				},
				"newer": {
					RowID:      2,
					ID:         "newer",
					Data:       json.RawMessage(`{"msg":"newer"}`),
					References: []string{"ref1"}, // Same number of references
				},
				"ref1": {
					RowID:      0,
					ID:         "ref1",
					Data:       json.RawMessage(`{"msg":"ref"}`),
					References: []string{},
				},
			},
			// Should pick newer (higher rowid) as end blob
			expectedOrder: []string{"ref1", "newer"},
		},
		{
			name: "all blobs have no references (fallback to highest rowid)",
			blobs: map[string]*CursorBlob{
				"blob1": {
					RowID:      5,
					ID:         "blob1",
					Data:       json.RawMessage(`{"msg":"blob1"}`),
					References: []string{},
				},
				"blob2": {
					RowID:      10,
					ID:         "blob2",
					Data:       json.RawMessage(`{"msg":"blob2"}`),
					References: []string{},
				},
				"blob3": {
					RowID:      3,
					ID:         "blob3",
					Data:       json.RawMessage(`{"msg":"blob3"}`),
					References: []string{},
				},
			},
			// Should pick blob2 (highest rowid) as the starting point
			expectedOrder: []string{"blob2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, orphaned, err := topologicalSort(tt.blobs)
			if err != nil {
				t.Errorf("topologicalSort() returned error: %v", err)
				return
			}

			// Convert result to IDs for comparison
			resultIDs := make([]string, len(result))
			for i, blob := range result {
				resultIDs[i] = blob.ID
			}

			// Check length first
			if len(resultIDs) != len(tt.expectedOrder) {
				t.Errorf("topologicalSort() returned %d blobs, expected %d", len(resultIDs), len(tt.expectedOrder))
				t.Errorf("Got: %v", resultIDs)
				t.Errorf("Expected: %v", tt.expectedOrder)
				return
			}

			// Check order
			for i, expectedID := range tt.expectedOrder {
				if resultIDs[i] != expectedID {
					t.Errorf("topologicalSort() result[%d] = %s, expected %s", i, resultIDs[i], expectedID)
					t.Errorf("Full result: %v", resultIDs)
					t.Errorf("Expected: %v", tt.expectedOrder)
					break
				}
			}

			// Additional validation: check that references point backwards
			seenIDs := make(map[string]bool)
			for _, blob := range result {
				// All referenced blobs should have been seen already
				for _, refID := range blob.References {
					if _, exists := tt.blobs[refID]; exists && !seenIDs[refID] {
						t.Errorf("Blob %s references %s which comes after it in the sorted order", blob.ID, refID)
					}
				}
				seenIDs[blob.ID] = true
			}

			// For the orphaned blobs test, verify that orphaned blobs are returned
			if tt.name == "orphaned blobs" {
				if len(orphaned) != 2 {
					t.Errorf("Expected 2 orphaned blobs, got %d", len(orphaned))
				}
				// Check that orphan1 and orphan2 are in the orphaned list
				orphanIDs := make(map[string]bool)
				for _, blob := range orphaned {
					orphanIDs[blob.ID] = true
				}
				if !orphanIDs["orphan1"] || !orphanIDs["orphan2"] {
					t.Errorf("Expected orphan1 and orphan2 to be in orphaned blobs, got: %v", orphanIDs)
				}
			}
		})
	}
}
