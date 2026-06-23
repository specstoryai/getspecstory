package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/config"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

// CreateSearchCommand builds `specstory search [query…]` — the read-first sibling of
// resume. It opens an always-on search over the session index; `↵` reads a session
// (glamour-rendered), and `r` resumes it through the same launch path as `resume`.
// See docs/SESSION-SEARCH.md.
func CreateSearchCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "search [query…]",
		Short: "Search and read your past coding-agent sessions",
		Long: `Full-text search across every session SpecStory has indexed, then read the match.

'search' opens an interactive search of your session history. Type to search, press enter to
read a session (rendered for the terminal), and press 'r' to resume it in an agent. Anything
after the command pre-seeds the query, e.g. 'specstory search max cpu'.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			registry := factory.GetRegistry()
			initialQuery := strings.TrimSpace(strings.Join(args, " "))

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			store, err := openOrBuildResumeIndex()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			if total, _ := store.Count(); total == 0 {
				fprintln(os.Stderr, "\nNo agent sessions indexed yet. Run an agent, then try again (or `specstory reindex`).")
				return nil
			}

			homeID, homeName, idErr := utils.ComputeProjectID(cwd)
			if idErr != nil || homeID == "" {
				homeID, homeName = unknownProjectID, filepath.Base(cwd)
			}
			homeSessions, _ := store.ListByProject(homeID)
			homeHasSessions := len(homeSessions) > 0

			agents := map[string]agentMeta{}
			var installed []agentChoice
			for _, id := range registry.ListIDs() {
				prov, err := registry.Get(id)
				if err != nil {
					continue
				}
				agents[id] = agentMeta{name: prov.Name(), accent: colorForAgent(id)}
				if prov.Check("").Success {
					installed = append(installed, agentChoice{id: id, provider: prov})
				}
			}

			viewMode, lastAgent := "dense", ""
			if cfg, _ := config.Load(nil); cfg != nil {
				viewMode = cfg.GetResumeViewMode()
				lastAgent = cfg.GetResumeLastAgent()
			}

			model := newSearchTUI(store, registry, agents, installed, homeID, homeName, homeHasSessions, "", lastAgent, initialQuery)
			final, err := tea.NewProgram(model).Run()
			if err != nil {
				return fmt.Errorf("search failed: %w", err)
			}
			rm := final.(searchTUI)
			if rm.result.cancelled || rm.result.session == nil {
				return nil // read-only session: nothing to launch
			}

			// The user asked to resume a found session — launch via the shared path.
			fromProv, err := registry.Get(rm.result.session.Agent)
			if err != nil {
				return fmt.Errorf("unknown source agent %q: %w", rm.result.session.Agent, err)
			}
			toProv, err := registry.Get(rm.result.targetID)
			if err != nil {
				return fmt.Errorf("unknown target agent %q: %w", rm.result.targetID, err)
			}
			_ = config.SaveResumePrefs(viewMode, rm.result.targetID)

			plan := &resumePlan{
				from:      fromProv,
				fromID:    rm.result.session.Agent,
				sessionID: rm.result.session.SessionID,
				to:        toProv,
				toID:      rm.result.targetID,
			}
			return launchResume(plan, cwd, resumeLaunchOpts{useUTC: true})
		},
	}
}
