package cursorcli

import (
	"log/slog"
)

// topologicalSort performs topological sorting on the blob graph
// Returns sorted blobs based on their reference relationships and orphaned blobs
func topologicalSort(blobs map[string]*CursorBlob) ([]*CursorBlob, []*CursorBlob, error) {
	// Find the blob with the most outgoing references (likely the end of conversation)
	var endBlob *CursorBlob
	maxRefs := 0
	for _, blob := range blobs {
		if len(blob.References) > maxRefs {
			maxRefs = len(blob.References)
			endBlob = blob
		} else if len(blob.References) == maxRefs && endBlob != nil {
			// If tied, prefer the one with higher rowid (more recent)
			if blob.RowID > endBlob.RowID {
				endBlob = blob
			}
		}
	}

	// If no blob has references, fall back to highest rowid
	if endBlob == nil {
		for _, blob := range blobs {
			if endBlob == nil || blob.RowID > endBlob.RowID {
				endBlob = blob
			}
		}
	}

	// Walk backwards from the end through references
	var sorted []*CursorBlob
	visited := make(map[string]bool)

	if endBlob != nil {
		// Safely truncate ID for logging
		idDisplay := endBlob.ID
		if len(idDisplay) > 16 {
			idDisplay = idDisplay[:16] + "..."
		}
		slog.Debug("Starting backward traversal from end blob",
			"id", idDisplay,
			"rowid", endBlob.RowID,
			"numRefs", len(endBlob.References))

		// Use recursive DFS to traverse backwards
		var traverse func(blobID string)
		traverse = func(blobID string) {
			if visited[blobID] {
				return
			}

			blob, exists := blobs[blobID]
			if !exists {
				return
			}

			visited[blobID] = true

			// First visit all referenced blobs (go deeper)
			for _, refID := range blob.References {
				traverse(refID)
			}

			// Then add this blob (post-order traversal gives us the right order)
			sorted = append(sorted, blob)
		}

		// Start traversal from the end blob
		traverse(endBlob.ID)
	}

	// Collect orphaned blobs (unreferenced duplicates)
	var orphaned []*CursorBlob
	for id, blob := range blobs {
		if !visited[id] {
			orphaned = append(orphaned, blob)
		}
	}

	if len(orphaned) > 0 {
		slog.Debug("Backward traversal complete, found orphaned blobs",
			"connected", len(sorted),
			"orphaned", len(orphaned))

		// Uncomment this for debugging DAG sorting, logs orphaned blob info
		for _, blob := range orphaned {
			// Safely truncate ID for logging
			idDisplay := blob.ID
			if len(idDisplay) > 16 {
				idDisplay = idDisplay[:16] + "..."
			}
			slog.Debug("Orphaned blob",
				"rowid", blob.RowID,
				"id", idDisplay)
		}
	}

	slog.Debug("Topological sort complete", "totalBlobs", len(blobs), "sortedBlobs", len(sorted))

	return sorted, orphaned, nil
}
