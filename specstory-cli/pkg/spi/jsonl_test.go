package spi

import (
	"bufio"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
)

// readAll drains a reader through ReadRecordLine, returning each record's text
// with a marker in place of any record rejected as oversized.
func readAll(t *testing.T, content string, limit int) []string {
	t.Helper()
	reader := bufio.NewReader(strings.NewReader(content))
	var got []string
	for {
		line, oversized, err := ReadRecordLine(reader, limit)
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("unexpected read error: %v", err)
		}
		if oversized {
			got = append(got, "<oversized>")
		} else if trimmed := strings.TrimRight(string(line), "\n"); trimmed != "" {
			got = append(got, trimmed)
		}
		if errors.Is(err, io.EOF) {
			return got
		}
	}
}

func TestReadRecordLine(t *testing.T) {
	big := strings.Repeat("x", 64)

	tests := []struct {
		name    string
		content string
		limit   int
		want    []string
	}{
		{
			name:    "records returned in file order",
			content: "first\nsecond\nthird\n",
			limit:   1024,
			want:    []string{"first", "second", "third"},
		},
		{
			name:    "final record without a trailing newline is still returned",
			content: "first\nlast",
			limit:   1024,
			want:    []string{"first", "last"},
		},
		{
			name:    "blank lines are returned as empty and dropped by the caller",
			content: "first\n\nsecond\n",
			limit:   1024,
			want:    []string{"first", "second"},
		},
		{
			// The guarantee that makes skipping safe: a poisoned record in the
			// middle of a transcript costs that record and nothing else.
			name:    "records on both sides of an oversized record survive",
			content: "before\n" + big + "\nafter\n",
			limit:   16,
			want:    []string{"before", "<oversized>", "after"},
		},
		{
			name:    "an oversized final record does not swallow the records before it",
			content: "before\n" + big,
			limit:   16,
			want:    []string{"before", "<oversized>"},
		},
		{
			name:    "a record exactly at the limit is kept",
			content: strings.Repeat("x", 15) + "\n",
			limit:   16,
			want:    []string{strings.Repeat("x", 15)},
		},
		{
			// One byte past the limit is the boundary the cap actually defends.
			name:    "a record one byte past the limit is rejected",
			content: strings.Repeat("x", 16) + "\n",
			limit:   16,
			want:    []string{"<oversized>"},
		},
		{
			name:    "empty input yields no records",
			content: "",
			limit:   16,
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readAll(t, tt.content, tt.limit)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d records %q, want %d %q", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("record %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestReadRecordLineBoundsAllocation is the regression this helper exists for.
// Capping the line after bufio.Reader.ReadString returns still allocates the
// whole oversized record first, so the cap must be enforced as the record is
// read. A reverted fix allocates the full 64MB here rather than the limit.
func TestReadRecordLineBoundsAllocation(t *testing.T) {
	const limit = 1 << 20
	content := strings.Repeat("x", 64<<20) + "\nkept\n"

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	reader := bufio.NewReader(strings.NewReader(content))
	line, oversized, err := ReadRecordLine(reader, limit)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if !oversized || line != nil {
		t.Fatalf("oversized=%v, len(line)=%d; want the record rejected and released", oversized, len(line))
	}

	runtime.ReadMemStats(&after)
	// A generous ceiling: the point is to separate "bounded by limit" from
	// "allocated all 64MB", not to pin an exact figure.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 16<<20 {
		t.Errorf("allocated %d bytes reading a %d-byte record against a %d-byte limit", allocated, 64<<20, limit)
	}

	// The oversized record was drained through its newline, not left mid-line.
	line, oversized, err = ReadRecordLine(reader, limit)
	if oversized || strings.TrimRight(string(line), "\n") != "kept" {
		t.Fatalf("next record = %q (oversized=%v, err=%v), want %q", line, oversized, err, "kept")
	}
}
