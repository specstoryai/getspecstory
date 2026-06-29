package cmd

import (
	"errors"
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

// TestApplyLoadedClearsLoadingAndPopulates verifies an async library fetch result is folded
// into the model: loading clears, rows populate, and an error is surfaced without rows.
func TestApplyLoadedClearsLoadingAndPopulates(t *testing.T) {
	m := skillsTUI{loading: true, height: 40}
	out := m.applyLoaded(skillsLoadedMsg{views: []skills.SkillView{
		{SkillRow: cloud.SkillRow{Name: "a", State: cloud.SkillStateReady}},
		{SkillRow: cloud.SkillRow{Name: "b", State: cloud.SkillStateReady}},
	}})
	if out.loading {
		t.Error("loading should be cleared after a load result")
	}
	if len(out.filtered) != 2 {
		t.Errorf("got %d rows, want 2", len(out.filtered))
	}

	errModel := skillsTUI{loading: true, height: 40}.applyLoaded(skillsLoadedMsg{err: errors.New("boom")})
	if errModel.loading {
		t.Error("loading should clear even on error")
	}
	if errModel.status == "" {
		t.Error("error status should be set")
	}
}

// TestApplyLoadedPreservesCursor ensures a post-action refresh keeps the selection on the
// same skill even if the order changed.
func TestApplyLoadedPreservesCursor(t *testing.T) {
	m := skillsTUI{height: 40}
	m.all = []skills.SkillView{
		{SkillRow: cloud.SkillRow{Name: "a", State: cloud.SkillStateReady}},
		{SkillRow: cloud.SkillRow{Name: "b", State: cloud.SkillStateReady}},
	}
	m.applyFilter()
	m.cursor = 1 // selecting "b"

	out := m.applyLoaded(skillsLoadedMsg{views: []skills.SkillView{
		{SkillRow: cloud.SkillRow{Name: "b", State: cloud.SkillStateReady}},
		{SkillRow: cloud.SkillRow{Name: "a", State: cloud.SkillStateReady}},
	}})
	if sel := out.selectedSkill(); sel == nil || sel.Name != "b" {
		t.Errorf("cursor did not follow 'b', got %v", sel)
	}
}

// TestApplyActionResult verifies busy clears, errors surface, and a reload is requested only
// on success.
func TestApplyActionResult(t *testing.T) {
	// Error: busy clears, status is the error, no reload command.
	model, cmd := skillsTUI{busy: true, height: 40}.applyActionResult(actionResultMsg{err: errors.New("nope")})
	m := model.(skillsTUI)
	if m.busy {
		t.Error("busy should clear on action result")
	}
	if cmd != nil {
		t.Error("no reload command expected on error")
	}
	if m.status != "nope" {
		t.Errorf("status = %q, want error text", m.status)
	}

	// Success with reload + scope: status set, scope remembered, reload command returned.
	model2, cmd2 := skillsTUI{busy: true, height: 40}.applyActionResult(actionResultMsg{
		status: "Installed x.", reload: true, scope: "project",
	})
	m2 := model2.(skillsTUI)
	if cmd2 == nil {
		t.Error("expected a reload command on success")
	}
	if m2.defaultLocation != "project" {
		t.Errorf("defaultLocation = %q, want project", m2.defaultLocation)
	}
	if m2.status != "Installed x." {
		t.Errorf("status = %q", m2.status)
	}
}

// TestApplyRunTriggered verifies a triggered run begins polling, and an error doesn't.
func TestApplyRunTriggered(t *testing.T) {
	ok, cmd := skillsTUI{busy: true, height: 40}.applyRunTriggered(runTriggeredMsg{runID: "r1"})
	m := ok.(skillsTUI)
	if !m.runActive || m.runID != "r1" {
		t.Errorf("expected runActive with id r1, got active=%v id=%q", m.runActive, m.runID)
	}
	if cmd == nil {
		t.Error("expected a poll command after a run starts")
	}

	failModel, failCmd := skillsTUI{busy: true, height: 40}.applyRunTriggered(runTriggeredMsg{err: errors.New("disabled")})
	fm := failModel.(skillsTUI)
	if fm.runActive {
		t.Error("a failed trigger should not start polling")
	}
	if failCmd != nil {
		t.Error("no poll command expected on trigger error")
	}
	if fm.status != "disabled" {
		t.Errorf("status = %q, want error text", fm.status)
	}
}

// TestApplyRunsFetched covers the poll outcomes: keep polling while running, stop + refresh
// on done, stop on failure, and ignore stale polls.
func TestApplyRunsFetched(t *testing.T) {
	base := func() skillsTUI { return skillsTUI{runActive: true, runID: "r1", height: 40} }

	// Still running → keep polling.
	_, cmd := base().applyRunsFetched(runsFetchedMsg{runID: "r1", runs: []cloud.Run{{ID: "r1", Status: "judging", SessionsMined: 5}}})
	if cmd == nil {
		t.Error("expected to keep polling while the run is in progress")
	}

	// Done → stop polling, refresh library.
	doneModel, doneCmd := base().applyRunsFetched(runsFetchedMsg{runID: "r1", runs: []cloud.Run{{ID: "r1", Status: "done", DossierTotal: 3}}})
	dm := doneModel.(skillsTUI)
	if dm.runActive {
		t.Error("run should be inactive once done")
	}
	if doneCmd == nil {
		t.Error("expected a library-reload command after a completed run")
	}

	// Failed → stop polling, no reload.
	failModel, failCmd := base().applyRunsFetched(runsFetchedMsg{runID: "r1", runs: []cloud.Run{{ID: "r1", Status: "failed", Error: "boom"}}})
	if failModel.(skillsTUI).runActive {
		t.Error("run should be inactive once failed")
	}
	if failCmd != nil {
		t.Error("no reload expected on failure")
	}

	// Stale poll (different run id) → ignored.
	_, staleCmd := base().applyRunsFetched(runsFetchedMsg{runID: "old", runs: nil})
	if staleCmd != nil {
		t.Error("a stale poll should be ignored")
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
