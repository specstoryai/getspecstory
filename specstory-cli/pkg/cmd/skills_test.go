package cmd

import (
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/skills"
)

func TestInstallOptionsFromFlags(t *testing.T) {
	tests := []struct {
		name       string
		global     bool
		project    bool
		wantGlobal bool
		wantErr    bool
	}{
		{"default is global", false, false, true, false},
		{"explicit global", true, false, true, false},
		{"project flips off global", false, true, false, false},
		{"both is an error", true, true, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := installOptionsFromFlags(tt.global, tt.project, nil)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if opts.Global != tt.wantGlobal {
				t.Errorf("Global = %v, want %v", opts.Global, tt.wantGlobal)
			}
		})
	}
}

// TestSkillsFilter verifies the state filter narrows the visible list and that the cursor
// stays in bounds — exercised without a network/engine by populating the model directly.
func TestSkillsFilter(t *testing.T) {
	m := skillsTUI{
		all: []skills.SkillView{
			{SkillRow: cloud.SkillRow{Name: "a", State: cloud.SkillStateReview}},
			{SkillRow: cloud.SkillRow{Name: "b", State: cloud.SkillStateReady}},
			{SkillRow: cloud.SkillRow{Name: "c", State: cloud.SkillStateReady}},
			{SkillRow: cloud.SkillRow{Name: "d", State: cloud.SkillStateInstalled}},
		},
		filterCycle: []string{"", cloud.SkillStateReview, cloud.SkillStateReady, cloud.SkillStateInstalled},
		height:      40,
	}

	m.applyFilter()
	if len(m.filtered) != 4 {
		t.Errorf("no filter: got %d, want 4", len(m.filtered))
	}

	m.filter = cloud.SkillStateReady
	m.applyFilter()
	if len(m.filtered) != 2 {
		t.Errorf("ready filter: got %d, want 2", len(m.filtered))
	}
	for _, v := range m.filtered {
		if v.State != cloud.SkillStateReady {
			t.Errorf("ready filter leaked a %s row", v.State)
		}
	}

	// Cursor must clamp to the smaller filtered set.
	m.filter = cloud.SkillStateInstalled
	m.cursor = 3
	m.applyFilter()
	if sel := m.selectedSkill(); sel == nil || sel.Name != "d" {
		t.Errorf("expected to select the single installed skill 'd', got %v", sel)
	}
}

// TestSkillsFilterCycle confirms the filter ring advances through all four states and wraps.
func TestSkillsFilterCycle(t *testing.T) {
	cycle := []string{"", cloud.SkillStateReview, cloud.SkillStateReady, cloud.SkillStateInstalled}
	cur := ""
	want := []string{"review", "ready", "installed", ""}
	for i, w := range want {
		cur = nextInCycle(cycle, cur)
		if cur != w {
			t.Errorf("step %d: got %q, want %q", i, cur, w)
		}
	}
}
