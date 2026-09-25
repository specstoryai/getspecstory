package cursorcli

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

func TestBlobDebugOwnershipMatchesWriterNames(t *testing.T) {
	for _, index := range []int{0, 1, 42, math.MaxInt} {
		for _, rowID := range []int{math.MinInt, -1, 0, 42, math.MaxInt} {
			if name := blobDebugName(index, rowID); !isBlobDebugName(name) {
				t.Errorf("writer filename %q not owned", name)
			}
		}
	}
	for _, name := range []string{"session-data.json", "1-notes.json", "orphan-notes.json", "1-01.json", "0-1.json", "../1-2.json", "1-2.json.bak"} {
		if isBlobDebugName(name) {
			t.Errorf("unrelated file %q owned", name)
		}
	}
}

func TestDebugRawRefreshClearsReorderedAndOrphanBlobs(t *testing.T) {
	testutil.IsolateDebugDir(t)
	blobs := []BlobRecord{
		{RowID: 10, Data: json.RawMessage(`{"unknownNative":{"keep":true}}`)},
		{RowID: 20, Data: json.RawMessage(`{"text":"second"}`)},
		{RowID: 30, Data: json.RawMessage(`{"text":"third"}`)},
	}
	export := func(records, orphans []BlobRecord) {
		t.Helper()
		data, err := json.Marshal(records)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeDebugOutput("refresh", string(data), orphans); err != nil {
			t.Fatal(err)
		}
	}
	export(blobs, []BlobRecord{{RowID: 40, Data: json.RawMessage(`{"text":"orphan"}`)}})
	dir := spi.GetDebugDir("refresh")
	// The orphan becomes connected, one blob moves, and two disappear.
	testutil.AssertDebugRefresh(t, dir,
		[]string{"1-10.json", "2-20.json", "3-30.json", "orphan-40.json"},
		[]string{"session-data.json", "1-notes.json", "orphan-notes.json"}, func() {
			export([]BlobRecord{{RowID: 40, Data: json.RawMessage(`{"text":"connected"}`)}, blobs[0]}, nil)
		})
	data, err := os.ReadFile(filepath.Join(dir, "2-10.json"))
	if err != nil || !json.Valid(data) || !strings.Contains(string(data), "\n    \"unknownNative\": {") {
		t.Fatalf("native fields/formatting lost: %s (%v)", data, err)
	}
}
