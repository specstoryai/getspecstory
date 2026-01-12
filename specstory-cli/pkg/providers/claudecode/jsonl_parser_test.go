package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestBuildDAGs_WithIsMetaRoot tests that sessions with isMeta root records are properly built into DAGs
func TestBuildDAGs_WithIsMetaRoot(t *testing.T) {
	tests := []struct {
		name         string
		records      []JSONLRecord
		expectedDAGs int
		description  string
		validate     func(t *testing.T, dags [][]JSONLRecord)
	}{
		{
			name: "Session with isMeta root record",
			records: []JSONLRecord{
				{
					Data: map[string]interface{}{
						"type":       "user",
						"uuid":       "meta-root",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:00Z",
						"isMeta":     true,
						"parentUuid": nil,
					},
				},
				{
					Data: map[string]interface{}{
						"type":       "user",
						"uuid":       "user-msg",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:01Z",
						"parentUuid": "meta-root",
					},
				},
				{
					Data: map[string]interface{}{
						"type":       "assistant",
						"uuid":       "assistant-msg",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:02Z",
						"parentUuid": "user-msg",
					},
				},
			},
			expectedDAGs: 1,
			description:  "Should create single DAG with all records including isMeta root",
			validate: func(t *testing.T, dags [][]JSONLRecord) {
				if len(dags) != 1 {
					t.Errorf("Expected 1 DAG, got %d", len(dags))
					return
				}
				dag := dags[0]
				if len(dag) != 3 {
					t.Errorf("Expected DAG with 3 records, got %d", len(dag))
					return
				}
				// Verify the root is the isMeta record
				if dag[0].Data["uuid"] != "meta-root" {
					t.Errorf("Expected first record to be meta-root, got %v", dag[0].Data["uuid"])
				}
				// Verify isMeta flag is preserved
				if dag[0].Data["isMeta"] != true {
					t.Errorf("Expected isMeta flag to be preserved")
				}
			},
		},
		{
			name: "Multiple sessions with isMeta roots",
			records: []JSONLRecord{
				// First session
				{
					Data: map[string]interface{}{
						"type":       "user",
						"uuid":       "meta-1",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:00Z",
						"isMeta":     true,
						"parentUuid": nil,
					},
				},
				{
					Data: map[string]interface{}{
						"type":       "user",
						"uuid":       "user-1",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:01Z",
						"parentUuid": "meta-1",
					},
				},
				// Second session
				{
					Data: map[string]interface{}{
						"type":       "user",
						"uuid":       "meta-2",
						"sessionId":  "session-2",
						"timestamp":  "2024-01-01T13:00:00Z",
						"isMeta":     true,
						"parentUuid": nil,
					},
				},
				{
					Data: map[string]interface{}{
						"type":       "assistant",
						"uuid":       "assistant-2",
						"sessionId":  "session-2",
						"timestamp":  "2024-01-01T13:00:01Z",
						"parentUuid": "meta-2",
					},
				},
			},
			expectedDAGs: 2,
			description:  "Should create separate DAGs for each session",
			validate: func(t *testing.T, dags [][]JSONLRecord) {
				if len(dags) != 2 {
					t.Errorf("Expected 2 DAGs, got %d", len(dags))
					return
				}
				// Check first DAG
				if len(dags[0]) != 2 {
					t.Errorf("Expected first DAG with 2 records, got %d", len(dags[0]))
				}
				// Check second DAG
				if len(dags[1]) != 2 {
					t.Errorf("Expected second DAG with 2 records, got %d", len(dags[1]))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewJSONLParser()
			dags := parser.buildDAGs(tt.records)

			if len(dags) != tt.expectedDAGs {
				t.Errorf("%s: Expected %d DAGs, got %d", tt.description, tt.expectedDAGs, len(dags))
			}

			if tt.validate != nil {
				tt.validate(t, dags)
			}
		})
	}
}

// TestBuildDAGs_OrphanedRecords tests that orphaned records (whose parents don't exist) are handled correctly
func TestBuildDAGs_OrphanedRecords(t *testing.T) {
	tests := []struct {
		name         string
		records      []JSONLRecord
		expectedDAGs int
		description  string
		validate     func(t *testing.T, dags [][]JSONLRecord)
	}{
		{
			name: "Orphaned record with missing parent",
			records: []JSONLRecord{
				{
					Data: map[string]interface{}{
						"type":       "user",
						"uuid":       "orphan-1",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:01Z",
						"parentUuid": "missing-parent", // Parent doesn't exist
					},
				},
				{
					Data: map[string]interface{}{
						"type":       "assistant",
						"uuid":       "child-of-orphan",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:02Z",
						"parentUuid": "orphan-1",
					},
				},
			},
			expectedDAGs: 0, // No DAGs should be created since there's no root
			description:  "Orphaned records should NOT create DAGs",
			validate: func(t *testing.T, dags [][]JSONLRecord) {
				if len(dags) != 0 {
					t.Errorf("Expected no DAGs for orphaned records, got %d", len(dags))
				}
			},
		},
		{
			name: "Mix of valid root and orphaned records",
			records: []JSONLRecord{
				// Valid chain
				{
					Data: map[string]interface{}{
						"type":       "user",
						"uuid":       "valid-root",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:00Z",
						"parentUuid": nil,
					},
				},
				{
					Data: map[string]interface{}{
						"type":       "user",
						"uuid":       "valid-child",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:01Z",
						"parentUuid": "valid-root",
					},
				},
				// Orphaned chain
				{
					Data: map[string]interface{}{
						"type":       "user",
						"uuid":       "orphan",
						"sessionId":  "session-2",
						"timestamp":  "2024-01-01T13:00:00Z",
						"parentUuid": "missing-parent",
					},
				},
			},
			expectedDAGs: 1, // Only the valid chain should create a DAG
			description:  "Should only create DAG for valid chain, ignore orphans",
			validate: func(t *testing.T, dags [][]JSONLRecord) {
				if len(dags) != 1 {
					t.Errorf("Expected 1 DAG, got %d", len(dags))
					return
				}
				dag := dags[0]
				if len(dag) != 2 {
					t.Errorf("Expected DAG with 2 records, got %d", len(dag))
					return
				}
				// Verify it's the valid chain
				if dag[0].Data["uuid"] != "valid-root" {
					t.Errorf("Expected valid-root as first record, got %v", dag[0].Data["uuid"])
				}
			},
		},
		{
			name: "Orphaned record that would have been root if parent existed",
			records: []JSONLRecord{
				{
					Data: map[string]interface{}{
						"type":       "user",
						"uuid":       "record-1",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:01Z",
						"parentUuid": "filtered-meta", // Parent was filtered (e.g., isMeta in old code)
					},
				},
				{
					Data: map[string]interface{}{
						"type":       "user",
						"uuid":       "record-2",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:02Z",
						"parentUuid": "record-1",
					},
				},
				{
					Data: map[string]interface{}{
						"type":       "assistant",
						"uuid":       "record-3",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:03Z",
						"parentUuid": "record-2",
					},
				},
			},
			expectedDAGs: 0, // Should NOT create a DAG
			description:  "Orphaned chains should not become roots",
			validate: func(t *testing.T, dags [][]JSONLRecord) {
				if len(dags) != 0 {
					t.Errorf("Expected no DAGs for orphaned chain, got %d", len(dags))
					for i, dag := range dags {
						t.Logf("DAG %d has %d records", i, len(dag))
						for j, record := range dag {
							t.Logf("  Record %d: uuid=%v, parentUuid=%v",
								j, record.Data["uuid"], record.Data["parentUuid"])
						}
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewJSONLParser()
			dags := parser.buildDAGs(tt.records)

			if len(dags) != tt.expectedDAGs {
				t.Errorf("%s: Expected %d DAGs, got %d", tt.description, tt.expectedDAGs, len(dags))
			}

			if tt.validate != nil {
				tt.validate(t, dags)
			}
		})
	}
}

// TestBuildDAGs_NormalChain tests normal DAG building with proper parent-child relationships
func TestBuildDAGs_NormalChain(t *testing.T) {
	tests := []struct {
		name         string
		records      []JSONLRecord
		expectedDAGs int
		description  string
		validate     func(t *testing.T, dags [][]JSONLRecord)
	}{
		{
			name: "Simple linear chain",
			records: []JSONLRecord{
				{
					Data: map[string]interface{}{
						"type":       "user",
						"uuid":       "root",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:00Z",
						"parentUuid": nil,
					},
				},
				{
					Data: map[string]interface{}{
						"type":       "assistant",
						"uuid":       "child-1",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:01Z",
						"parentUuid": "root",
					},
				},
				{
					Data: map[string]interface{}{
						"type":       "user",
						"uuid":       "child-2",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:02Z",
						"parentUuid": "child-1",
					},
				},
			},
			expectedDAGs: 1,
			description:  "Should create single DAG with all records in order",
			validate: func(t *testing.T, dags [][]JSONLRecord) {
				if len(dags) != 1 {
					t.Errorf("Expected 1 DAG, got %d", len(dags))
					return
				}
				dag := dags[0]
				if len(dag) != 3 {
					t.Errorf("Expected DAG with 3 records, got %d", len(dag))
					return
				}
				// After flattening, records should be in timestamp order
				expectedOrder := []string{"root", "child-1", "child-2"}
				for i, expected := range expectedOrder {
					if dag[i].Data["uuid"] != expected {
						t.Errorf("Expected record %d to be %s, got %v", i, expected, dag[i].Data["uuid"])
					}
				}
			},
		},
		{
			name: "Multiple roots create multiple DAGs",
			records: []JSONLRecord{
				{
					Data: map[string]interface{}{
						"type":       "user",
						"uuid":       "root-1",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:00Z",
						"parentUuid": nil,
					},
				},
				{
					Data: map[string]interface{}{
						"type":       "user",
						"uuid":       "root-2",
						"sessionId":  "session-2",
						"timestamp":  "2024-01-01T13:00:00Z",
						"parentUuid": nil,
					},
				},
				{
					Data: map[string]interface{}{
						"type":       "assistant",
						"uuid":       "child-of-1",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:01Z",
						"parentUuid": "root-1",
					},
				},
				{
					Data: map[string]interface{}{
						"type":       "assistant",
						"uuid":       "child-of-2",
						"sessionId":  "session-2",
						"timestamp":  "2024-01-01T13:00:01Z",
						"parentUuid": "root-2",
					},
				},
			},
			expectedDAGs: 2,
			description:  "Should create separate DAG for each root",
			validate: func(t *testing.T, dags [][]JSONLRecord) {
				if len(dags) != 2 {
					t.Errorf("Expected 2 DAGs, got %d", len(dags))
					return
				}
				// Each DAG should have 2 records
				for i, dag := range dags {
					if len(dag) != 2 {
						t.Errorf("DAG %d: Expected 2 records, got %d", i, len(dag))
					}
				}
			},
		},
		{
			name: "Out of order records should still build correct DAG",
			records: []JSONLRecord{
				{
					Data: map[string]interface{}{
						"type":       "user",
						"uuid":       "child-2",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:02Z",
						"parentUuid": "child-1",
					},
				},
				{
					Data: map[string]interface{}{
						"type":       "user",
						"uuid":       "root",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:00Z",
						"parentUuid": nil,
					},
				},
				{
					Data: map[string]interface{}{
						"type":       "assistant",
						"uuid":       "child-1",
						"sessionId":  "session-1",
						"timestamp":  "2024-01-01T12:00:01Z",
						"parentUuid": "root",
					},
				},
			},
			expectedDAGs: 1,
			description:  "Should build correct DAG regardless of input order",
			validate: func(t *testing.T, dags [][]JSONLRecord) {
				if len(dags) != 1 {
					t.Errorf("Expected 1 DAG, got %d", len(dags))
					return
				}
				dag := dags[0]
				if len(dag) != 3 {
					t.Errorf("Expected DAG with 3 records, got %d", len(dag))
					return
				}
				// Verify parent-child relationships are preserved
				for _, record := range dag {
					uuid := record.Data["uuid"].(string)
					parentUuid := record.Data["parentUuid"]
					if uuid == "root" && parentUuid != nil {
						t.Errorf("Root should have nil parent")
					}
					if uuid == "child-1" && parentUuid != "root" {
						t.Errorf("child-1 should have root as parent")
					}
					if uuid == "child-2" && parentUuid != "child-1" {
						t.Errorf("child-2 should have child-1 as parent")
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewJSONLParser()
			dags := parser.buildDAGs(tt.records)

			if len(dags) != tt.expectedDAGs {
				t.Errorf("%s: Expected %d DAGs, got %d", tt.description, tt.expectedDAGs, len(dags))
			}

			if tt.validate != nil {
				tt.validate(t, dags)
			}
		})
	}
}

// TestFlattenDAG tests that DAGs are correctly flattened and sorted by timestamp
func TestFlattenDAG(t *testing.T) {
	parser := NewJSONLParser()

	dag := []JSONLRecord{
		{
			Data: map[string]interface{}{
				"uuid":       "c",
				"timestamp":  "2024-01-01T12:00:02Z",
				"parentUuid": "b",
			},
		},
		{
			Data: map[string]interface{}{
				"uuid":       "a",
				"timestamp":  "2024-01-01T12:00:00Z",
				"parentUuid": nil,
			},
		},
		{
			Data: map[string]interface{}{
				"uuid":       "b",
				"timestamp":  "2024-01-01T12:00:01Z",
				"parentUuid": "a",
			},
		},
	}

	flattened := parser.flattenDAG(dag)

	if len(flattened) != 3 {
		t.Errorf("Expected 3 records after flattening, got %d", len(flattened))
	}

	// Check order - should be sorted by timestamp
	expectedOrder := []string{"a", "b", "c"}
	for i, expected := range expectedOrder {
		if flattened[i].Data["uuid"] != expected {
			t.Errorf("Expected record %d to be %s, got %v", i, expected, flattened[i].Data["uuid"])
		}
	}
}

// TestEliminateDuplicates tests that duplicate records are properly eliminated
func TestEliminateDuplicates(t *testing.T) {
	parser := NewJSONLParser()

	records := []JSONLRecord{
		{
			Data: map[string]interface{}{
				"uuid":      "duplicate",
				"timestamp": "2024-01-01T12:00:01Z", // Later timestamp
				"content":   "version 2",
			},
		},
		{
			Data: map[string]interface{}{
				"uuid":      "duplicate",
				"timestamp": "2024-01-01T12:00:00Z", // Earlier timestamp - should be kept
				"content":   "version 1",
			},
		},
		{
			Data: map[string]interface{}{
				"uuid":      "unique",
				"timestamp": "2024-01-01T12:00:00Z",
				"content":   "unique record",
			},
		},
		{
			Data: map[string]interface{}{
				// Record without UUID should be skipped
				"timestamp": "2024-01-01T12:00:00Z",
				"content":   "no uuid",
			},
		},
	}

	deduped := parser.eliminateDuplicates(records)

	// Should have 2 records (duplicate merged, no-uuid skipped)
	if len(deduped) != 2 {
		t.Errorf("Expected 2 records after deduplication, got %d", len(deduped))
	}

	// Find the duplicate record
	var duplicateRecord *JSONLRecord
	for i := range deduped {
		if deduped[i].Data["uuid"] == "duplicate" {
			duplicateRecord = &deduped[i]
			break
		}
	}

	if duplicateRecord == nil {
		t.Errorf("Duplicate record not found after deduplication")
	} else {
		// Should have kept the earlier timestamp version
		if duplicateRecord.Data["content"] != "version 1" {
			t.Errorf("Expected to keep version 1 (earlier timestamp), got %v", duplicateRecord.Data["content"])
		}
	}
}

// TestMergeDagsWithSameSessionId tests that DAGs with the same session ID are properly merged
func TestMergeDagsWithSameSessionId(t *testing.T) {
	tests := []struct {
		name          string
		dags          [][]JSONLRecord
		expectedCount int
		description   string
		validate      func(t *testing.T, merged [][]JSONLRecord)
	}{
		{
			name: "Multiple roots with same session ID should merge",
			dags: [][]JSONLRecord{
				// First DAG from first root (like line 1-327 of the problem file)
				{
					JSONLRecord{
						Data: map[string]interface{}{
							"type":       "user",
							"uuid":       "root-1",
							"sessionId":  "69286dbb-266a-413a-ac4b-f68de7042f40",
							"timestamp":  "2025-08-06T18:51:59.247Z",
							"parentUuid": nil,
						},
					},
					JSONLRecord{
						Data: map[string]interface{}{
							"type":       "user",
							"uuid":       "child-1-1",
							"sessionId":  "69286dbb-266a-413a-ac4b-f68de7042f40",
							"timestamp":  "2025-08-06T18:52:00.000Z",
							"parentUuid": "root-1",
						},
					},
				},
				// Second DAG from second root (like line 328+ of the problem file)
				{
					JSONLRecord{
						Data: map[string]interface{}{
							"type":       "user",
							"uuid":       "root-2",
							"sessionId":  "69286dbb-266a-413a-ac4b-f68de7042f40", // Same session ID!
							"timestamp":  "2025-08-06T19:26:59.248Z",
							"parentUuid": nil,
						},
					},
					JSONLRecord{
						Data: map[string]interface{}{
							"type":       "assistant",
							"uuid":       "child-2-1",
							"sessionId":  "69286dbb-266a-413a-ac4b-f68de7042f40",
							"timestamp":  "2025-08-06T19:27:00.000Z",
							"parentUuid": "root-2",
						},
					},
				},
			},
			expectedCount: 1, // Should merge into single DAG
			description:   "Two DAGs with same session ID should merge into one",
			validate: func(t *testing.T, merged [][]JSONLRecord) {
				if len(merged) != 1 {
					t.Errorf("Expected 1 merged DAG, got %d", len(merged))
					return
				}
				// Should have all 4 records
				if len(merged[0]) != 4 {
					t.Errorf("Expected merged DAG to have 4 records, got %d", len(merged[0]))
				}
				// Verify all records are present
				uuids := make(map[string]bool)
				for _, record := range merged[0] {
					if uuid, ok := record.Data["uuid"].(string); ok {
						uuids[uuid] = true
					}
				}
				expectedUuids := []string{"root-1", "child-1-1", "root-2", "child-2-1"}
				for _, expected := range expectedUuids {
					if !uuids[expected] {
						t.Errorf("Missing expected UUID in merged DAG: %s", expected)
					}
				}
			},
		},
		{
			name: "Multiple roots with different session IDs should not merge",
			dags: [][]JSONLRecord{
				// First DAG with session-1
				{
					JSONLRecord{
						Data: map[string]interface{}{
							"type":       "user",
							"uuid":       "root-1",
							"sessionId":  "session-1",
							"timestamp":  "2025-08-06T18:51:59.247Z",
							"parentUuid": nil,
						},
					},
					JSONLRecord{
						Data: map[string]interface{}{
							"type":       "user",
							"uuid":       "child-1",
							"sessionId":  "session-1",
							"timestamp":  "2025-08-06T18:52:00.000Z",
							"parentUuid": "root-1",
						},
					},
				},
				// Second DAG with session-2
				{
					JSONLRecord{
						Data: map[string]interface{}{
							"type":       "user",
							"uuid":       "root-2",
							"sessionId":  "session-2", // Different session ID
							"timestamp":  "2025-08-06T19:26:59.248Z",
							"parentUuid": nil,
						},
					},
					JSONLRecord{
						Data: map[string]interface{}{
							"type":       "assistant",
							"uuid":       "child-2",
							"sessionId":  "session-2",
							"timestamp":  "2025-08-06T19:27:00.000Z",
							"parentUuid": "root-2",
						},
					},
				},
			},
			expectedCount: 2, // Should remain separate
			description:   "Two DAGs with different session IDs should remain separate",
			validate: func(t *testing.T, merged [][]JSONLRecord) {
				if len(merged) != 2 {
					t.Errorf("Expected 2 separate DAGs, got %d", len(merged))
					return
				}
				// Each should have 2 records
				for i, dag := range merged {
					if len(dag) != 2 {
						t.Errorf("DAG %d: Expected 2 records, got %d", i, len(dag))
					}
				}
			},
		},
		{
			name: "Three DAGs, two with same session ID",
			dags: [][]JSONLRecord{
				{
					JSONLRecord{
						Data: map[string]interface{}{
							"uuid":       "dag1-root",
							"sessionId":  "session-A",
							"timestamp":  "2025-01-01T10:00:00Z",
							"parentUuid": nil,
						},
					},
				},
				{
					JSONLRecord{
						Data: map[string]interface{}{
							"uuid":       "dag2-root",
							"sessionId":  "session-B",
							"timestamp":  "2025-01-01T11:00:00Z",
							"parentUuid": nil,
						},
					},
				},
				{
					JSONLRecord{
						Data: map[string]interface{}{
							"uuid":       "dag3-root",
							"sessionId":  "session-A", // Same as first
							"timestamp":  "2025-01-01T12:00:00Z",
							"parentUuid": nil,
						},
					},
				},
			},
			expectedCount: 2, // dag1 and dag3 merge, dag2 stays separate
			description:   "Should merge only DAGs with matching session IDs",
			validate: func(t *testing.T, merged [][]JSONLRecord) {
				if len(merged) != 2 {
					t.Errorf("Expected 2 DAGs after merging, got %d", len(merged))
				}
				// Find the merged DAG (should have 2 records)
				var mergedDag, separateDag []JSONLRecord
				for _, dag := range merged {
					if len(dag) == 2 {
						mergedDag = dag
					} else if len(dag) == 1 {
						separateDag = dag
					}
				}
				if mergedDag == nil {
					t.Errorf("Expected to find merged DAG with 2 records")
				}
				if separateDag == nil {
					t.Errorf("Expected to find separate DAG with 1 record")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewJSONLParser()
			merged := parser.mergeDagsWithSameSessionId(tt.dags)

			if len(merged) != tt.expectedCount {
				t.Errorf("%s: Expected %d DAGs after merging, got %d",
					tt.description, tt.expectedCount, len(merged))
			}

			if tt.validate != nil {
				tt.validate(t, merged)
			}
		})
	}
}

// TestBuildDAGs_LargeDataset tests DAG building performance with 10,000 records
// This ensures the algorithm handles large datasets without stack overflow
func TestBuildDAGs_LargeDataset(t *testing.T) {
	tests := []struct {
		name         string
		recordCount  int
		description  string
		sessionCount int
		validate     func(t *testing.T, dags [][]JSONLRecord, recordCount int)
	}{
		{
			name:         "Single session with 10000 records",
			recordCount:  10000,
			sessionCount: 1,
			description:  "Should handle deep chain without stack overflow",
			validate: func(t *testing.T, dags [][]JSONLRecord, recordCount int) {
				if len(dags) != 1 {
					t.Errorf("Expected 1 DAG, got %d", len(dags))
					return
				}
				if len(dags[0]) != recordCount {
					t.Errorf("Expected DAG with %d records, got %d", recordCount, len(dags[0]))
				}
			},
		},
		{
			name:         "Multiple sessions totaling 10000 records",
			recordCount:  10000,
			sessionCount: 100, // 100 sessions with 100 records each
			description:  "Should handle many separate DAGs efficiently",
			validate: func(t *testing.T, dags [][]JSONLRecord, recordCount int) {
				if len(dags) != 100 {
					t.Errorf("Expected 100 DAGs, got %d", len(dags))
					return
				}
				totalRecords := 0
				for _, dag := range dags {
					totalRecords += len(dag)
				}
				if totalRecords != recordCount {
					t.Errorf("Expected %d total records across DAGs, got %d", recordCount, totalRecords)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var records []JSONLRecord

			if tt.sessionCount == 1 {
				// Generate a single deep chain
				records = generateDeepChain(tt.recordCount, "session-1")
			} else {
				// Generate multiple sessions
				records = generateMultipleSessions(tt.recordCount, tt.sessionCount)
			}

			// Time the DAG building to ensure reasonable performance
			startTime := time.Now()
			parser := NewJSONLParser()
			dags := parser.buildDAGs(records)
			elapsed := time.Since(startTime)

			// Performance check - should complete within reasonable time
			maxDuration := 20 * time.Second
			if elapsed > maxDuration {
				t.Errorf("%s: DAG building took %v, exceeding max duration of %v",
					tt.description, elapsed, maxDuration)
			}

			t.Logf("%s: Built %d DAGs from %d records in %v",
				tt.name, len(dags), tt.recordCount, elapsed)

			if tt.validate != nil {
				tt.validate(t, dags, tt.recordCount)
			}

			// Verify no corruption in the DAG structure
			for i, dag := range dags {
				if !verifyDAGIntegrity(dag) {
					t.Errorf("DAG %d has integrity issues", i)
				}
			}
		})
	}
}

// generateDeepChain creates a linear chain of records for testing
func generateDeepChain(count int, sessionId string) []JSONLRecord {
	records := make([]JSONLRecord, count)

	// Create root record
	records[0] = JSONLRecord{
		Data: map[string]interface{}{
			"type":       "user",
			"uuid":       fmt.Sprintf("record-%d", 0),
			"sessionId":  sessionId,
			"timestamp":  fmt.Sprintf("2024-01-01T12:00:%02d.%03dZ", 0, 0),
			"parentUuid": nil,
			"message": map[string]interface{}{
				"role":    "user",
				"content": "Start of conversation",
			},
		},
	}

	// Create chain of records
	for i := 1; i < count; i++ {
		recordType := "user"
		if i%2 == 0 {
			recordType = "assistant"
		}

		// Format timestamp with proper seconds and milliseconds
		seconds := (i / 1000) % 60
		millis := i % 1000
		minutes := (i / 60000) % 60
		hours := 12 + (i / 3600000)

		records[i] = JSONLRecord{
			Data: map[string]interface{}{
				"type":       recordType,
				"uuid":       fmt.Sprintf("record-%d", i),
				"sessionId":  sessionId,
				"timestamp":  fmt.Sprintf("2024-01-01T%02d:%02d:%02d.%03dZ", hours, minutes, seconds, millis),
				"parentUuid": fmt.Sprintf("record-%d", i-1),
				"message": map[string]interface{}{
					"role":    recordType,
					"content": fmt.Sprintf("Message %d", i),
				},
			},
		}
	}

	return records
}

// generateMultipleSessions creates multiple sessions with records
func generateMultipleSessions(totalRecords int, sessionCount int) []JSONLRecord {
	records := make([]JSONLRecord, 0, totalRecords)
	recordsPerSession := totalRecords / sessionCount

	for session := 0; session < sessionCount; session++ {
		sessionId := fmt.Sprintf("session-%d", session)

		// Create root for this session
		records = append(records, JSONLRecord{
			Data: map[string]interface{}{
				"type":       "user",
				"uuid":       fmt.Sprintf("s%d-record-0", session),
				"sessionId":  sessionId,
				"timestamp":  fmt.Sprintf("2024-01-01T%02d:00:00.000Z", session%24),
				"parentUuid": nil,
				"message": map[string]interface{}{
					"role":    "user",
					"content": fmt.Sprintf("Start of session %d", session),
				},
			},
		})

		// Add remaining records for this session
		for i := 1; i < recordsPerSession && len(records) < totalRecords; i++ {
			recordType := "user"
			if i%2 == 0 {
				recordType = "assistant"
			}

			records = append(records, JSONLRecord{
				Data: map[string]interface{}{
					"type":       recordType,
					"uuid":       fmt.Sprintf("s%d-record-%d", session, i),
					"sessionId":  sessionId,
					"timestamp":  fmt.Sprintf("2024-01-01T%02d:00:%02d.%03dZ", session%24, i%60, i%1000),
					"parentUuid": fmt.Sprintf("s%d-record-%d", session, i-1),
					"message": map[string]interface{}{
						"role":    recordType,
						"content": fmt.Sprintf("Session %d, Message %d", session, i),
					},
				},
			})
		}
	}

	return records
}

// verifyDAGIntegrity checks that a DAG maintains proper parent-child relationships
func verifyDAGIntegrity(dag []JSONLRecord) bool {
	if len(dag) == 0 {
		return true
	}

	// Build a map of UUIDs for quick lookup
	uuidMap := make(map[string]bool)
	for _, record := range dag {
		if uuid, ok := record.Data["uuid"].(string); ok {
			uuidMap[uuid] = true
		}
	}

	// Verify each record's parent exists in the DAG (except root)
	for _, record := range dag {
		parentUuid := record.Data["parentUuid"]
		if parentUuid == nil {
			continue // Root record
		}

		if parentStr, ok := parentUuid.(string); ok {
			if !uuidMap[parentStr] {
				return false // Parent doesn't exist in DAG
			}
		}
	}

	return true
}

// TestParseLargeJSONLLines tests that the parser can handle JSONL lines of various sizes
// Regression test for https://github.com/specstoryai/getspecstory/issues/108
func TestParseLargeJSONLLines(t *testing.T) {
	tests := []struct {
		name        string
		lineSizeKB  int
		shouldPass  bool
		description string
		omitNewline bool
	}{
		{
			name:        "10KB line",
			lineSizeKB:  10,
			shouldPass:  true,
			description: "Should handle 10KB lines easily",
		},
		{
			name:        "100KB line",
			lineSizeKB:  100,
			shouldPass:  true,
			description: "Should handle 100KB lines",
		},
		{
			name:        "1MB line",
			lineSizeKB:  1024,
			shouldPass:  true,
			description: "Should handle 1MB lines",
		},
		{
			name:        "10MB line",
			lineSizeKB:  10240,
			shouldPass:  true,
			description: "Should handle 10MB lines (current buffer limit)",
		},
		{
			name:        "50MB line",
			lineSizeKB:  51200,
			shouldPass:  true, // Should handle arbitrarily large lines
			description: "Very large 50MB line - should handle without limits",
		},
		{
			name:        "line without trailing newline",
			lineSizeKB:  10,
			shouldPass:  true,
			description: "Should handle final line without newline at EOF",
			omitNewline: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary JSONL file with a large line
			tmpFile, err := os.CreateTemp("", "test-large-line-*.jsonl")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer func() { _ = os.Remove(tmpFile.Name()) }()

			// Generate a large JSON object with a string field that's approximately the target size
			// Account for JSON overhead (field names, quotes, braces, etc.)
			targetBytes := tt.lineSizeKB * KB
			// Subtract overhead for JSON structure: {"type":"user","uuid":"test-large","sessionId":"test","timestamp":"...","parentUuid":null,"largeField":"..."}
			overhead := 200 // Approximate overhead in bytes
			contentSize := targetBytes - overhead
			if contentSize < 0 {
				contentSize = targetBytes
			}

			// Generate large content string
			largeContent := strings.Repeat("x", contentSize)

			// Create valid JSONL record
			record := map[string]interface{}{
				"type":       "user",
				"uuid":       "test-large",
				"sessionId":  "test-session",
				"timestamp":  "2024-01-01T12:00:00.000Z",
				"parentUuid": nil,
				"largeField": largeContent,
			}

			jsonBytes, err := json.Marshal(record)
			if err != nil {
				t.Fatalf("Failed to marshal JSON: %v", err)
			}

			actualSizeKB := len(jsonBytes) / KB
			t.Logf("Generated line size: %d KB (target: %d KB)", actualSizeKB, tt.lineSizeKB)

			// Write to file
			if _, err := tmpFile.Write(jsonBytes); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			// Only write newline if not testing omitNewline case
			if !tt.omitNewline {
				if _, err := tmpFile.WriteString("\n"); err != nil {
					t.Fatalf("Failed to write newline: %v", err)
				}
			}
			_ = tmpFile.Close()

			// Try to parse the file
			parser := NewJSONLParser()
			records, err := parser.parseSessionFile(tmpFile.Name())

			if err != nil {
				if tt.shouldPass {
					t.Errorf("%s: Expected to parse successfully but got error: %v", tt.description, err)
					// Check if it's the specific bufio.Scanner error
					if strings.Contains(err.Error(), "token too long") {
						t.Errorf("  → Hit 'bufio.Scanner: token too long' error at %d KB", actualSizeKB)
					}
				} else {
					t.Logf("%s: Failed as expected with error: %v", tt.description, err)
				}
			} else {
				if !tt.shouldPass {
					t.Logf("%s: Unexpectedly succeeded (parsed %d records)", tt.description, len(records))
				} else {
					t.Logf("%s: Successfully parsed %d records", tt.description, len(records))
				}

				// Verify we got the record
				if len(records) != 1 {
					t.Errorf("Expected 1 record, got %d", len(records))
				}

				// Verify the large field was preserved
				if len(records) > 0 {
					if largeField, ok := records[0].Data["largeField"].(string); ok {
						if len(largeField) != len(largeContent) {
							t.Errorf("Large field size mismatch: expected %d, got %d", len(largeContent), len(largeField))
						}
					} else {
						t.Errorf("Large field missing or wrong type")
					}
				}
			}
		})
	}
}

// TestExtractSessionIDFromFile tests extraction of session IDs from JSONL files
func TestExtractSessionIDFromFile(t *testing.T) {
	tests := []struct {
		name        string
		fileContent string
		expectedID  string
	}{
		{
			name:        "sessionId on line 1",
			fileContent: `{"sessionId":"a1b2c3d4-e5f6-7890-abcd-ef1234567890","uuid":"msg-1","type":"user"}` + "\n",
			expectedID:  "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
		},
		{
			name: "sessionId on line 3",
			fileContent: `{"uuid":"msg-1","type":"user"}` + "\n" +
				`{"uuid":"msg-2","type":"assistant"}` + "\n" +
				`{"sessionId":"12345678-1234-5678-9abc-def012345678","uuid":"msg-3","type":"user"}` + "\n",
			expectedID: "12345678-1234-5678-9abc-def012345678",
		},
		{
			name:        "empty file",
			fileContent: "",
			expectedID:  "",
		},
		{
			name: "no sessionId in any line",
			fileContent: `{"uuid":"msg-1","type":"user"}` + "\n" +
				`{"uuid":"msg-2","type":"assistant"}` + "\n",
			expectedID: "",
		},
		{
			name:        "invalid JSON (regex ignores structure)",
			fileContent: `{not valid json}` + "\n",
			expectedID:  "",
		},
		{
			name:        "sessionId is null",
			fileContent: `{"sessionId":null,"uuid":"msg-1","type":"user"}` + "\n",
			expectedID:  "",
		},
		{
			name:        "sessionId is empty string",
			fileContent: `{"sessionId":"","uuid":"msg-1","type":"user"}` + "\n",
			expectedID:  "",
		},
		{
			name:        "sessionId is number (wrong type)",
			fileContent: `{"sessionId":12345,"uuid":"msg-1","type":"user"}` + "\n",
			expectedID:  "",
		},
		{
			name: "empty lines before sessionId",
			fileContent: "\n\n" +
				`{"sessionId":"abcdef12-3456-7890-abcd-ef1234567890","uuid":"msg-1"}` + "\n",
			expectedID: "abcdef12-3456-7890-abcd-ef1234567890",
		},
		{
			name:        "file without trailing newline",
			fileContent: `{"sessionId":"fedcba98-7654-3210-fedc-ba9876543210","uuid":"msg-1"}`,
			expectedID:  "fedcba98-7654-3210-fedc-ba9876543210",
		},
		{
			name:        "sessionId is non-UUID string (not matched)",
			fileContent: `{"sessionId":"not-a-valid-uuid","uuid":"msg-1","type":"user"}` + "\n",
			expectedID:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file with test content
			tmpFile, err := os.CreateTemp("", "test-extract-session-*.jsonl")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer func() { _ = os.Remove(tmpFile.Name()) }()

			if _, err := tmpFile.WriteString(tt.fileContent); err != nil {
				t.Fatalf("Failed to write test content: %v", err)
			}
			_ = tmpFile.Close()

			// Call the function under test
			sessionID, err := extractSessionIDFromFile(tmpFile.Name())
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if sessionID != tt.expectedID {
				t.Errorf("Expected sessionID %q, got %q", tt.expectedID, sessionID)
			}
		})
	}
}

// TestExtractSessionIDFromFile_NonexistentFile tests error handling for missing files
func TestExtractSessionIDFromFile_NonexistentFile(t *testing.T) {
	_, err := extractSessionIDFromFile("/nonexistent/path/to/file.jsonl")
	if err == nil {
		t.Errorf("Expected error for nonexistent file, got none")
	}
	if !strings.Contains(err.Error(), "failed to open file") {
		t.Errorf("Expected 'failed to open file' error, got: %v", err)
	}
}

// TestParseJSONLLineWithEmbeddedNewlines verifies that the parser correctly handles
// JSON strings containing escaped newlines (\n) without treating them as line boundaries.
// This ensures ReadString('\n') only breaks on actual newline bytes, not escaped sequences.
func TestParseJSONLLineWithEmbeddedNewlines(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		description string
	}{
		{
			name:        "single embedded newline",
			content:     "Line 1\nLine 2",
			description: "Should handle content with one embedded newline",
		},
		{
			name:        "multiple embedded newlines",
			content:     "Line 1\nLine 2\nLine 3\nLine 4",
			description: "Should handle content with multiple embedded newlines",
		},
		{
			name:        "newlines at start and end",
			content:     "\nContent in middle\n",
			description: "Should handle newlines at boundaries",
		},
		{
			name:        "many consecutive newlines",
			content:     "Start\n\n\n\nEnd",
			description: "Should handle multiple consecutive newlines",
		},
		{
			name:        "mixed whitespace with newlines",
			content:     "Line 1\n\tTabbed line\n    Spaced line\n",
			description: "Should handle newlines mixed with other whitespace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary JSONL file
			tmpFile, err := os.CreateTemp("", "test-embedded-newlines-*.jsonl")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer func() { _ = os.Remove(tmpFile.Name()) }()

			// Create a JSONL record with content containing actual newlines
			// When marshaled, these will become \n escape sequences in the JSON string
			record := map[string]interface{}{
				"type":      "user",
				"uuid":      "test-newlines",
				"sessionId": "test-session",
				"timestamp": "2024-01-01T12:00:00.000Z",
				"content":   tt.content, // This will be escaped when marshaled
			}

			jsonBytes, err := json.Marshal(record)
			if err != nil {
				t.Fatalf("Failed to marshal JSON: %v", err)
			}

			// Verify the JSON contains escaped newlines, not actual newlines
			jsonStr := string(jsonBytes)
			if strings.Count(jsonStr, "\n") > 0 {
				t.Fatalf("Marshaled JSON should not contain actual newlines, only escaped \\n sequences")
			}
			if !strings.Contains(jsonStr, "\\n") && strings.Contains(tt.content, "\n") {
				t.Fatalf("Marshaled JSON should contain escaped \\n for content with newlines")
			}

			// Write the JSON line followed by a newline
			if _, err := tmpFile.Write(jsonBytes); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			if _, err := tmpFile.WriteString("\n"); err != nil {
				t.Fatalf("Failed to write newline: %v", err)
			}
			_ = tmpFile.Close()

			// Parse the file
			parser := NewJSONLParser()
			records, err := parser.parseSessionFile(tmpFile.Name())

			if err != nil {
				t.Errorf("%s: Expected to parse successfully but got error: %v", tt.description, err)
				return
			}

			// Verify we got exactly one record (not split by embedded newlines)
			if len(records) != 1 {
				t.Errorf("%s: Expected 1 record, got %d - embedded newlines may have split the record", tt.description, len(records))
				return
			}

			// Verify the content field was preserved with newlines intact
			if content, ok := records[0].Data["content"].(string); ok {
				if content != tt.content {
					t.Errorf("%s: Content mismatch\nExpected: %q\nGot: %q", tt.description, tt.content, content)
					// Show the difference more clearly
					t.Errorf("Expected %d chars with %d newlines, got %d chars with %d newlines",
						len(tt.content), strings.Count(tt.content, "\n"),
						len(content), strings.Count(content, "\n"))
				}
			} else {
				t.Errorf("%s: Content field missing or wrong type", tt.description)
			}
		})
	}
}
