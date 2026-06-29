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

// CreateSearchCommand builds `specstory search [query…]`. It is the same interactive UI as
// `specstory resume` (see sessionTUI), entered straight into the all-projects full-text
// search with the input focused: type to search, `space` previews a match (glamour-rendered),
// and `r` resumes it through the same launch path as `resume`. See docs/SESSION-SEARCH.md.
func CreateSearchCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "search [query…]",
		Short: "Search and read your past coding-agent sessions",
		Long: `Full-text search across every session SpecStory has indexed, then read the match.

'search' opens an interactive search of your session history. Type to search, press space to
preview a session (rendered for the terminal), and press 'r' to resume it in an agent. Anything
after the command pre-seeds the query, e.g. 'specstory search max cpu'.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			registry := factory.GetRegistry()
			initialQuery := strings.TrimSpace(strings.Join(args, " "))

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			store, builtFresh, err := openOrBuildResumeIndex()
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

			// `search` is the same TUI as `resume`, entered straight into the all-projects
			// FTS with the input focused. See newSessionTUI / sessionTUIOpts.
			model := newSessionTUI(store, registry, homeID, homeName, homeSessions, agents, installed, sessionTUIOpts{
				title:         "SpecStory Search",
				lastAgent:     lastAgent,
				viewMode:      viewMode,
				initialQuery:  initialQuery,
				startInSearch: true,
			})
			p := tea.NewProgram(model)
			cancelWarm := startIndexWarm(p, homeID, builtFresh)
			final, err := p.Run()
			cancelWarm() // stop warming the moment search exits (never block on it)
			if err != nil {
				return fmt.Errorf("search failed: %w", err)
			}
			rm, ok := final.(sessionTUI)
			if !ok {
				return fmt.Errorf("search returned unexpected model type %T", final)
			}
			if rm.result.cancelled || rm.result.session == nil {
				return nil // cancelled: nothing to launch
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
				// A search hit can be from another project; resume must load the source from
				// its original cwd, not the current directory.
				fromCwd: rm.result.session.OriginCwd,
				to:      toProv,
				toID:    rm.result.targetID,
			}
			return launchResume(plan, cwd, resumeLaunchOpts{useUTC: true})
		},
	}
}
