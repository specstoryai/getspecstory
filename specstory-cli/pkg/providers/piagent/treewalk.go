package piagent

// leafPathEntries walks from the leaf (last entry in file order) to the root,
// reverses to chronological order, and applies compaction: if a compaction
// entry is on the path, entries before its firstKeptEntryId are dropped.
func leafPathEntries(entries []rawEntry) []rawEntry {
	byID := indexByID(entries)
	leaf := entries[len(entries)-1]
	path := walkToRoot(leaf, byID)
	reverse(path)
	return applyCompaction(path)
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

// applyCompaction drops entries before the most recent compaction point when
// one or more compaction entries are on the path. pi's buildContextEntries
// builds context from the LATEST compaction's firstKeptEntryId forward (each
// new compaction summarizes prior compactions into itself), so when multiple
// compactions exist we use the last one in chronological order, not the first.
// If firstKeptEntryId is missing from the path, we keep from the compaction
// entry forward so pre-compaction entries are never accidentally retained.
func applyCompaction(path []rawEntry) []rawEntry {
	last := -1
	for i, e := range path {
		if e.Type == entryCompaction {
			last = i
		}
	}
	if last < 0 {
		return path
	}
	keptID := compactionFirstKept(path[last])
	if keptID == "" {
		return path[last:]
	}
	if idx := keepFromIndex(path, keptID); idx >= 0 {
		return path[idx:]
	}
	return path[last:]
}

// compactionFirstKept returns the firstKeptEntryId of a compaction entry. pi
// stores this as a top-level field on the entry (not inside a message
// payload), so it is decoded directly into rawEntry.FirstKeptEntryID.
func compactionFirstKept(e rawEntry) string {
	return e.FirstKeptEntryID
}

// keepFromIndex returns the index of the entry with id==keptID in path, or -1
// if not found.
func keepFromIndex(path []rawEntry, keptID string) int {
	for i, e := range path {
		if e.ID == keptID {
			return i
		}
	}
	return -1
}
