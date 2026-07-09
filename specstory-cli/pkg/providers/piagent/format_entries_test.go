package piagent

import (
	"strings"
	"testing"
)

// These tests verify coverage of the non-message entry types, the tree
// structure, and session versions from the pi session file format
// (https://pi.dev/docs/latest/session-format), using full_format.jsonl
// (control entries + compaction), branching.jsonl (tree), and v1_legacy.jsonl.

// TestFormatEntries_ControlEntriesSkipped covers the non-message entry types:
// model_change, thinking_level_change, session_info, label, custom, and
// custom_message. They must not break the parse and must not produce exchange
// messages (custom_message content is extension context, not a user turn).
func TestFormatEntries_ControlEntriesSkipped(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "full_format.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	if !data.Validate() {
		t.Error("Validate() returned false; a control entry leaked an invalid message")
	}
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				if strings.Contains(part.Text, "Injected context from extension") {
					t.Error("custom_message content leaked into exchanges as a user turn")
				}
				if strings.Contains(part.Text, "branch explored approach B") {
					t.Error("branch_summary content leaked into exchanges")
				}
			}
		}
	}
}

// TestFormatEntries_BranchSummaryEntry covers the top-level branch_summary entry
// (type:"branch_summary" with fromId, summary, details, fromHook). It must be
// skipped from exchanges without error.
func TestFormatEntries_BranchSummaryEntry(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "full_format.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				if strings.Contains(part.Text, "explored approach B") {
					t.Error("top-level branch_summary leaked into exchanges")
				}
			}
		}
	}
}

// TestFormatEntries_CompactionDropsPreKept covers the compaction entry's
// firstKeptEntryId field on full_format.jsonl: firstKeptEntryId=a2, so the
// common answer (before a2) is dropped and the post-compaction prompt survives.
func TestFormatEntries_CompactionDropsPreKept(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "full_format.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var hasCommon, hasPost bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				if strings.Contains(part.Text, "common answer") {
					hasCommon = true
				}
				if strings.Contains(part.Text, "after compaction prompt") {
					hasPost = true
				}
			}
		}
	}
	if hasCommon {
		t.Error("entry before firstKeptEntryId was not dropped by compaction")
	}
	if !hasPost {
		t.Error("post-compaction prompt was dropped")
	}
}

// TestFormatEntries_CompactionHonorsFirstKeptEntryId asserts that when a
// compaction entry is on the leaf path, entries before firstKeptEntryId are
// dropped and entries from firstKeptEntryId forward are kept. The fixture's
// compaction points at a1, so u1 (pre-kept) must NOT appear, while a1 (the kept
// entry) and u2 (post-compaction) MUST appear.
func TestFormatEntries_CompactionHonorsFirstKeptEntryId(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "compaction.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var hasDroppedPre, hasKept, hasPost bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				switch {
				case strings.Contains(part.Text, "first prompt before compaction"):
					hasDroppedPre = true
				case strings.Contains(part.Text, "first answer before compaction"):
					hasKept = true
				case strings.Contains(part.Text, "after compaction"):
					hasPost = true
				}
			}
		}
	}
	if hasDroppedPre {
		t.Error("pre-kept user prompt (u1) was not dropped by compaction")
	}
	if !hasKept {
		t.Error("kept entry (a1, firstKeptEntryId) was dropped")
	}
	if !hasPost {
		t.Error("post-compaction user prompt was dropped by compaction")
	}
}

// TestFormatEntries_CompactionMissingKeptIdDropsPreCompaction asserts the
// fallback: when firstKeptEntryId is not found in the leaf path, pre-compaction
// entries are still dropped (kept from the compaction entry forward).
func TestFormatEntries_CompactionMissingKeptIdDropsPreCompaction(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "compaction_missing.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var hasPre, hasPost bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				if strings.Contains(part.Text, "before compaction") {
					hasPre = true
				}
				if strings.Contains(part.Text, "after compaction") {
					hasPost = true
				}
			}
		}
	}
	if hasPre {
		t.Error("pre-compaction user prompt retained when firstKeptEntryId was missing")
	}
	if !hasPost {
		t.Error("post-compaction user prompt was dropped in the missing-kept-id fallback")
	}
}
