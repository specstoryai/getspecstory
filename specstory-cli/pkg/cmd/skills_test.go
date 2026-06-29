package cmd

import (
	"errors"
	"testing"

	"charm.land/bubbles/v2/spinner"

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

// TestApplyRunTriggered verifies a triggered run switches to the Runs tab and refreshes;
// an error does neither.
func TestApplyRunTriggered(t *testing.T) {
	ok, cmd := skillsTUI{busy: true, height: 40}.applyRunTriggered(runTriggeredMsg{runID: "r1"})
	m := ok.(skillsTUI)
	if m.runID != "r1" || m.tab != tabRuns || !m.runsLoading {
		t.Errorf("expected runs tab + loading with id r1, got tab=%v loading=%v id=%q", m.tab, m.runsLoading, m.runID)
	}
	if cmd == nil {
		t.Error("expected a runs-fetch command after a run starts")
	}

	failModel, failCmd := skillsTUI{busy: true, height: 40}.applyRunTriggered(runTriggeredMsg{err: errors.New("disabled")})
	fm := failModel.(skillsTUI)
	if fm.tab == tabRuns || fm.runsLoading {
		t.Error("a failed trigger should not switch to the runs tab")
	}
	if failCmd != nil {
		t.Error("no command expected on trigger error")
	}
	if fm.status != "disabled" {
		t.Errorf("status = %q, want error text", fm.status)
	}
}

// TestApplyRunsFetched covers the poll loop: start polling while a run is in progress, stop
// and refresh the library on the transition to all-terminal, and surface load errors.
func TestApplyRunsFetched(t *testing.T) {
	// Initial load with an in-progress run → start polling.
	loadModel, loadCmd := (skillsTUI{runsLoading: true, height: 40}).
		applyRunsFetched(runsFetchedMsg{runs: []cloud.Run{{ID: "r1", Status: "judging", SessionsMined: 5}}})
	lm := loadModel.(skillsTUI)
	if !lm.runsLoaded || lm.runsLoading {
		t.Error("expected runsLoaded and not loading after a fetch")
	}
	if !lm.runsPolling || !lm.runsInProgress {
		t.Error("expected polling to start for an in-progress run")
	}
	if loadCmd == nil {
		t.Error("expected a poll command while a run is in progress")
	}

	// Transition to done (a run was in progress) → stop polling, refresh library.
	doneModel, doneCmd := (skillsTUI{runsInProgress: true, runsPolling: true, height: 40}).
		applyRunsFetched(runsFetchedMsg{runs: []cloud.Run{{ID: "r1", Status: "done", DossierTotal: 3}}})
	dm := doneModel.(skillsTUI)
	if dm.runsPolling || dm.runsInProgress {
		t.Error("polling should stop once all runs are terminal")
	}
	if doneCmd == nil {
		t.Error("expected a library-reload command after a run completes")
	}

	// Steady state, nothing in progress → no polling, no command.
	idleModel, idleCmd := (skillsTUI{height: 40}).
		applyRunsFetched(runsFetchedMsg{runs: []cloud.Run{{ID: "r1", Status: "done"}}})
	if idleModel.(skillsTUI).runsPolling {
		t.Error("no polling expected when nothing is in progress")
	}
	if idleCmd != nil {
		t.Error("no command expected in steady state")
	}

	// Load error → surfaced in status.
	errModel, _ := (skillsTUI{runsLoading: true, height: 40}).
		applyRunsFetched(runsFetchedMsg{err: errors.New("nope")})
	if errModel.(skillsTUI).status == "" {
		t.Error("expected a load error to be surfaced in status")
	}
}

// TestSwitchTabLoadsRunsOnce verifies the runs list is fetched lazily on first switch.
func TestSwitchTabLoadsRunsOnce(t *testing.T) {
	m := skillsTUI{height: 40, spinner: spinner.New()}
	out, cmd := m.switchTab(tabRuns)
	om := out.(skillsTUI)
	if om.tab != tabRuns || !om.runsLoading || cmd == nil {
		t.Errorf("first switch should load runs: tab=%v loading=%v cmd=%v", om.tab, om.runsLoading, cmd != nil)
	}
	// Already loaded → no refetch.
	om.runsLoading = false
	om.runsLoaded = true
	back, _ := om.switchTab(tabLibrary)
	again, cmd2 := back.(skillsTUI).switchTab(tabRuns)
	if again.(skillsTUI).runsLoading || cmd2 != nil {
		t.Error("a second switch should not reload an already-loaded runs list")
	}
}

// TestSkillsFilterCycle confirms the filter ring advances through all four states and wraps.

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
