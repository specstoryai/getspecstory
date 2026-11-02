package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/updater"
)

// Exercise Cobra's inherited flag parsing without accessing releases or the
// running test executable. Silent mode must still run the requested operation.
func TestUpdateSilent(t *testing.T) {
	for _, scenario := range []struct {
		name, flag, message string
		status              updater.Status
	}{
		{"install", "", "Updated SpecStory to 2.0.0", updater.Status{Installed: "2.0.0"}},
		{"current", "", "SpecStory 1.0.0 is up to date", updater.Status{Installed: "1.0.0"}},
		{"check", "--check", "Latest stable: 2.0.0", updater.Status{Latest: "2.0.0"}},
		{"rollback", "--rollback", "Restored SpecStory 0.9.0", updater.Status{Installed: "0.9.0"}},
	} {
		for _, mode := range []struct {
			name          string
			defaultSilent bool
			args          []string
			wantSilent    bool
		}{
			{name: "normal"},
			{name: "flag", args: []string{"--silent"}, wantSilent: true},
			{name: "config", defaultSilent: true, wantSilent: true},
			{name: "override", defaultSilent: true, args: []string{"--silent=false"}},
		} {
			t.Run(scenario.name+"/"+mode.name, func(t *testing.T) {
				root := &cobra.Command{Use: "specstory"}
				root.PersistentFlags().Bool("silent", mode.defaultSilent, "suppress non-error output")
				var output bytes.Buffer
				root.SetOut(&output)
				called := false
				root.AddCommand(createUpdateCommand("1.0.0", func(_ context.Context, check, rollback bool) (updater.Status, error) {
					called = true
					if check != (scenario.flag == "--check") || rollback != (scenario.flag == "--rollback") {
						t.Fatalf("wrong operation: check=%v rollback=%v", check, rollback)
					}
					return scenario.status, nil
				}))
				args := append([]string{"update"}, mode.args...)
				if scenario.flag != "" {
					args = append(args, scenario.flag)
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				if !called {
					t.Fatal("silent mode skipped the operation")
				}
				if mode.wantSilent && output.Len() != 0 {
					t.Fatalf("silent command printed %q", output.String())
				}
				if !mode.wantSilent && !strings.Contains(output.String(), scenario.message) {
					t.Fatalf("missing status: %q", output.String())
				}
			})
		}
	}
}

func TestUpdateSilentPreservesErrors(t *testing.T) {
	want := errors.New("download failed")
	root := &cobra.Command{Use: "specstory", SilenceUsage: true}
	root.PersistentFlags().Bool("silent", false, "suppress non-error output")
	var output, diagnostic bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&diagnostic)
	root.AddCommand(createUpdateCommand("1.0.0", func(context.Context, bool, bool) (updater.Status, error) {
		return updater.Status{}, want
	}))
	root.SetArgs([]string{"update", "--silent"})
	if err := root.Execute(); !errors.Is(err, want) {
		t.Fatalf("lost update error: %v", err)
	}
	if output.Len() != 0 || !strings.Contains(diagnostic.String(), want.Error()) {
		t.Fatalf("stdout=%q stderr=%q", output.String(), diagnostic.String())
	}
}
