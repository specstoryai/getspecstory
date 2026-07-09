package piagent

import "encoding/json"

// leafPathEntries walks from the leaf (last entry in file order) to the root,
// reverses to chronological order, and applies compaction: if a compaction
// entry is on the path, entries before its firstKeptEntryId are dropped.
func leafPathEntries(entries []rawEntry) []rawEntry {
	byID := indexByID(entries)
	leaf := entries[len(entries)-1]
	path := walkToRoot(leaf, byID)
	reverse(path)
	return applyCompaction(path, byID)
}

// indexByID builds an id -> entry lookup for the tree walk.
func indexByID(entries []rawEntry) map[string]rawEntry {
	m := make(map[string]rawEntry, len(entries))
	for _, e := range entries {
		m[e.ID] = e
	}
	return m
}

// walkToRoot collects entries from the given leaf up to the root (parentId null).
func walkToRoot(leaf rawEntry, byID map[string]rawEntry) []rawEntry {
	var path []rawEntry
	cur := leaf
	for {
		path = append(path, cur)
		if cur.ParentID == nil {
			break
		}
		parent, ok := byID[*cur.ParentID]
		if !ok {
			break
		}
		cur = parent
	}
	return path
}

// reverse reverses a slice of rawEntry in place.
func reverse(s []rawEntry) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// applyCompaction drops entries before the compaction point when a compaction
// entry is on the path. If no compaction is present, the path is returned as-is.
func applyCompaction(path []rawEntry, _ map[string]rawEntry) []rawEntry {
	for i, e := range path {
		if e.Type != entryCompaction {
			continue
		}
		keptID := compactionFirstKept(e)
		if keptID == "" {
			return path[i:]
		}
		return path[keepFromIndex(path, keptID):]
	}
	return path
}

// compactionFirstKept extracts firstKeptEntryId from a compaction entry's raw
// message payload. Returns "" if absent.
func compactionFirstKept(e rawEntry) string {
	var p struct {
		FirstKeptEntryID string `json:"firstKeptEntryId"`
	}
	_ = json.Unmarshal(e.Message, &p)
	return p.FirstKeptEntryID
}

// keepFromIndex returns the index of the entry with id==keptID in path, or 0
// if not found (defensive: keep everything from the compaction point).
func keepFromIndex(path []rawEntry, keptID string) int {
	for i, e := range path {
		if e.ID == keptID {
			return i
		}
	}
	return 0
}
