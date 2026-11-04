package factory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/antigravitycli"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/claudecode"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/codexcli"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/copilotcli"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/copilotide"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/cursorcli"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/deepseektui"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/droidcli"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/geminicli"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/grokbuild"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/musecode"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/piagent"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/qwencode"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// TestRegisterAll_VariantRegistersViaUserDataDirOverride guards the startup
// ordering that --user-data-dir depends on: Copilot IDE variants are only
// registered when their storage holds at least one chat, so overrides must be
// in effect before registerAll runs (main() pre-parses the flag for exactly
// this reason — cobra's RunE fires only after the registry has initialized).
// With an override pointing at a fake VSCodium install containing a chat
// session, a fresh registry must register the copilotide-vscodium provider.
func TestRegisterAll_VariantRegistersViaUserDataDirOverride(t *testing.T) {
	variant := copilotide.VSCodium

	// If the host has a real install with chats, the variant registers with or
	// without the override and the assertion below would prove nothing.
	if copilotide.HasAnyChatSessions(variant) {
		t.Skipf("host has a real %s install with Copilot chats; cannot isolate the override path", variant.AppName)
	}

	// Fake user-data-dir with one workspace holding a chatSessions directory —
	// the marker HasAnyChatSessions gates variant registration on.
	userDataDir := t.TempDir()
	chatSessions := filepath.Join(userDataDir, "User", "workspaceStorage", "ws1", "chatSessions")
	if err := os.MkdirAll(chatSessions, 0755); err != nil {
		t.Fatalf("Failed to create fake chatSessions: %v", err)
	}

	copilotide.SetUserDataDirOverride(variant.ID, userDataDir)
	t.Cleanup(func() { copilotide.SetUserDataDirOverride(variant.ID, "") })

	// A fresh Registry rather than the global singleton: the singleton's
	// registration may already have run without the override in other tests.
	r := &Registry{providers: make(map[string]spi.Provider)}
	r.registerAll()

	if _, ok := r.providers[variant.ID]; !ok {
		t.Errorf("variant %q not registered with a valid --user-data-dir override; registered providers: %v",
			variant.ID, r.ListIDsUnsafe())
	}
}

func TestAgentExitChild(t *testing.T) {
	if value := os.Getenv("SPECSTORY_EXIT_TEST_CODE"); value != "" {
		code, _ := strconv.Atoi(value)
		if cwdFile := os.Getenv("SPECSTORY_EXIT_TEST_CWD_FILE"); cwdFile != "" {
			cwd, err := os.Getwd()
			if err != nil {
				os.Exit(93)
			}
			if err := os.WriteFile(cwdFile, []byte(cwd), 0600); err != nil {
				os.Exit(94)
			}
		}
		if destination := os.Getenv("SPECSTORY_EXIT_TEST_DESTINATION"); destination != "" {
			data, err := os.ReadFile(os.Getenv("SPECSTORY_EXIT_TEST_SOURCE"))
			if err != nil {
				os.Exit(90)
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
				os.Exit(91)
			}
			if err := os.WriteFile(destination, data, 0600); err != nil {
				os.Exit(92)
			}
		}
		os.Exit(code)
	}
}

// useTempAgentStores keeps a test's fake agent out of the developer's real
// session stores: a new home, and no inherited store override to escape it.
func useTempAgentStores(t *testing.T) {
	t.Helper()
	testutil.SetHome(t, t.TempDir())
	for _, key := range []string{
		"CLAUDE_CONFIG_DIR", "CODEX_HOME", "COPILOT_HOME", "GROK_HOME", "QWEN_HOME", "QWEN_RUNTIME_DIR",
		"PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR", "XDG_DATA_HOME", "OPENCODE_DB",
	} {
		t.Setenv(key, "")
	}
}

// agentLaunchers lists every provider that starts a terminal agent.
func agentLaunchers() []struct {
	name string
	run  func(projectPath, command, id string) error
} {
	return []struct {
		name string
		run  func(projectPath, command, id string) error
	}{
		{"claude", claudecode.ExecuteClaude},
		{"codex", codexcli.ExecuteCodex},
		{"copilot", copilotcli.ExecuteCopilot},
		{"cursor", cursorcli.ExecuteCursorCLI},
		{"gemini", geminicli.ExecuteGemini},
		{"grok", grokbuild.ExecuteGrok},
		{"droid", droidcli.ExecuteDroid},
		{"deepseek", deepseektui.ExecuteDeepSeek},
		{"antigravity", antigravitycli.ExecuteAntigravity},
		{"pi", piagent.ExecutePi},
		{"muse", musecode.ExecuteMuse},
		{"qwen", qwencode.ExecuteQwen},
	}
}

func TestTerminalAgentExitStatus(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := fmt.Sprintf("%q -test.run=^TestAgentExitChild$", exe)
	for _, tt := range agentLaunchers() {
		t.Run(tt.name, func(t *testing.T) {
			for _, code := range []int{0, 7} {
				t.Setenv("SPECSTORY_EXIT_TEST_CODE", strconv.Itoa(code))
				err := tt.run(t.TempDir(), command, "")
				if code == 0 {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					var exit *spi.AgentExitError
					if !errors.As(err, &exit) || exit.Code != code {
						t.Fatalf("got %v, want agent exit %d", err, code)
					}
				}
			}
			var exit *spi.AgentExitError
			if err := tt.run(t.TempDir(), filepath.Join(t.TempDir(), "missing-agent"), ""); err == nil || errors.As(err, &exit) {
				t.Fatalf("launch failure classified as agent exit: %v", err)
			}
		})
	}
}

// Agents key sessions to the directory they run in, so with --project-path the
// agent must run in the project or the watcher never sees its sessions.
func TestTerminalAgentRunsInProjectDirectory(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := fmt.Sprintf("%q -test.run=^TestAgentExitChild$", exe)
	for _, tt := range agentLaunchers() {
		t.Run(tt.name, func(t *testing.T) {
			project := t.TempDir()
			cwdFile := filepath.Join(t.TempDir(), "cwd")
			t.Setenv("SPECSTORY_EXIT_TEST_CODE", "0")
			t.Setenv("SPECSTORY_EXIT_TEST_CWD_FILE", cwdFile)

			if err := tt.run(project, command, ""); err != nil {
				t.Fatal(err)
			}
			cwd, err := os.ReadFile(cwdFile)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.Stat(string(cwd))
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.Stat(project)
			if err != nil {
				t.Fatal(err)
			}
			// SameFile: temp paths differ by symlink and case between platforms.
			if !os.SameFile(got, want) {
				t.Errorf("agent ran in %q, want %q", cwd, project)
			}
		})
	}
}

// With a non-local --project-path the agent stays in the launch directory and
// records that as its project, so the session must be followed there.
func TestRunSavesSessionWhenProjectPathIsNotLocal(t *testing.T) {
	for _, tt := range []struct {
		name     string
		provider spi.Provider
	}{
		{"claude", claudecode.NewProvider()},
		{"codex", codexcli.NewProvider()},
		{"copilot", copilotcli.NewProvider()},
		{"cursor", cursorcli.NewProvider()},
		{"gemini", geminicli.NewProvider()},
		{"grok", grokbuild.NewProvider()},
		{"droid", droidcli.NewProvider()},
		{"deepseek", deepseektui.NewProvider()},
		{"pi", piagent.NewProvider()},
		{"muse", musecode.NewProvider()},
		{"qwen", qwencode.NewProvider()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			useTempAgentStores(t)
			launchDir := spi.CanonicalizePathOrClean(t.TempDir())
			t.Chdir(launchDir)
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			native, err := tt.provider.ReconstructSession(&schema.SessionData{
				SchemaVersion: schema.CurrentSchemaVersion,
				SessionID:     "source-session",
				WorkspaceRoot: launchDir,
				Exchanges: []schema.Exchange{{Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "work in the launch directory"}}},
				}}},
			}, spi.ReconstructOptions{WorkspaceRoot: launchDir})
			if err != nil {
				t.Fatal(err)
			}
			destination, err := tt.provider.NativeSessionPath(launchDir, native.Filename)
			if err != nil {
				t.Fatal(err)
			}
			staged := filepath.Join(t.TempDir(), "staged-session")
			if err := os.WriteFile(staged, native.Content, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SPECSTORY_EXIT_TEST_SOURCE", staged)
			t.Setenv("SPECSTORY_EXIT_TEST_DESTINATION", destination)
			t.Setenv("SPECSTORY_EXIT_TEST_CODE", "0")

			var mu sync.Mutex
			saved := make(map[string]bool)
			command := fmt.Sprintf("%q -test.run=^TestAgentExitChild$", exe)
			err = tt.provider.ExecAgentAndWatch("/no/such/remote/project", command, "", false, func(session *spi.AgentChatSession) {
				mu.Lock()
				defer mu.Unlock()
				saved[session.SessionID] = true
			})
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !saved[native.SessionID] {
				t.Errorf("session was not saved; delivered: %v", saved)
			}
		})
	}
}

// A remote workspace's root must not gain the current drive on its way to the
// launcher, or the agent starts in whatever local directory that names.
func TestDrivelessRemoteRootKeepsAgentInLaunchDirectoryOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows has rooted paths without a drive")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := fmt.Sprintf("%q -test.run=^TestAgentExitChild$", exe)
	for _, id := range []string{"copilot", "opencode"} {
		t.Run(id, func(t *testing.T) {
			useTempAgentStores(t)
			provider, err := GetRegistry().Get(id)
			if err != nil {
				t.Fatal(err)
			}
			unrelated := t.TempDir()
			launchDir := t.TempDir()
			t.Chdir(launchDir)
			cwdFile := filepath.Join(t.TempDir(), "cwd")
			t.Setenv("SPECSTORY_EXIT_TEST_CODE", "0")
			t.Setenv("SPECSTORY_EXIT_TEST_CWD_FILE", cwdFile)

			driveless := unrelated[len(filepath.VolumeName(unrelated)):]
			err = provider.ExecAgentAndWatch(driveless, command, "", false, func(*spi.AgentChatSession) {})
			if err != nil {
				t.Fatal(err)
			}
			cwd, err := os.ReadFile(cwdFile)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.Stat(string(cwd))
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.Stat(launchDir)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(got, want) {
				t.Errorf("agent ran in %q, want the launch directory %q", cwd, launchDir)
			}
		})
	}
}

// A child that writes and exits immediately can outrun fsnotify delivery. Run
// each provider twice to also exercise a stopped watcher's fresh context.
func TestRunFinalWriteAndWatcherRestart(t *testing.T) {
	for _, tt := range []struct {
		name     string
		provider spi.Provider
	}{
		{"claude", claudecode.NewProvider()},
		{"codex", codexcli.NewProvider()},
		{"gemini", geminicli.NewProvider()},
		{"deepseek", deepseektui.NewProvider()},
		{"droid", droidcli.NewProvider()},
		{"muse", musecode.NewProvider()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			useTempAgentStores(t)
			project := spi.CanonicalizePathOrClean(t.TempDir())
			t.Chdir(project)
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := fmt.Sprintf("%q -test.run=^TestAgentExitChild$", exe)
			for attempt := 0; attempt < 2; attempt++ {
				data := &schema.SessionData{
					SchemaVersion: schema.CurrentSchemaVersion,
					SessionID:     "source-session",
					WorkspaceRoot: project,
					Exchanges: []schema.Exchange{{Messages: []schema.Message{
						{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "last turn before exit"}}},
					}}},
				}
				native, err := tt.provider.ReconstructSession(data, spi.ReconstructOptions{WorkspaceRoot: project})
				if err != nil {
					t.Fatal(err)
				}
				destination, err := tt.provider.NativeSessionPath(project, native.Filename)
				if err != nil {
					t.Fatal(err)
				}
				staged := filepath.Join(t.TempDir(), "staged-session")
				if err := os.WriteFile(staged, native.Content, 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("SPECSTORY_EXIT_TEST_SOURCE", staged)
				t.Setenv("SPECSTORY_EXIT_TEST_DESTINATION", destination)
				t.Setenv("SPECSTORY_EXIT_TEST_CODE", "7")
				var mu sync.Mutex
				saved := make(map[string]bool)
				err = tt.provider.ExecAgentAndWatch(project, command, "", false, func(session *spi.AgentChatSession) {
					mu.Lock()
					defer mu.Unlock()
					saved[session.SessionID] = strings.Contains(session.RawData, "last turn before exit")
				})
				var exit *spi.AgentExitError
				if !errors.As(err, &exit) || exit.Code != 7 {
					t.Fatalf("got %v, want agent status 7", err)
				}
				mu.Lock()
				ok := saved[native.SessionID]
				count := len(saved)
				mu.Unlock()
				if !ok {
					t.Fatalf("final write was not saved on run %d", attempt+1)
				}
				if count != 1 {
					t.Fatalf("republished existing history: %v", saved)
				}
			}
		})
	}
}

func TestWatchAgentRestart(t *testing.T) {
	for _, tt := range []struct {
		name     string
		provider spi.Provider
	}{
		{"claude", claudecode.NewProvider()},
		{"codex", codexcli.NewProvider()},
		{"gemini", geminicli.NewProvider()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			useTempAgentStores(t)
			project := spi.CanonicalizePathOrClean(t.TempDir())
			t.Chdir(project)
			data := &schema.SessionData{SessionID: "source", WorkspaceRoot: project, Exchanges: []schema.Exchange{{Messages: []schema.Message{
				{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "live watcher update"}}},
			}}}}
			native, err := tt.provider.ReconstructSession(data, spi.ReconstructOptions{WorkspaceRoot: project})
			if err != nil {
				t.Fatal(err)
			}
			path, err := tt.provider.NativeSessionPath(project, native.Filename)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, native.Content, 0600); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				ctx, cancel := context.WithCancel(context.Background())
				updates := make(chan struct{}, 1)
				done := make(chan error, 1)
				go func() {
					done <- tt.provider.WatchAgent(ctx, project, false, func(s *spi.AgentChatSession) {
						if s.SessionID == native.SessionID {
							select {
							case updates <- struct{}{}:
							default:
							}
						}
					})
				}()
				// Watch registration is asynchronous in these providers. Repeat
				// a real write until an event arrives, bounded by a deadline.
				ticker := time.NewTicker(20 * time.Millisecond)
				deadline := time.NewTimer(5 * time.Second)
				received := false
			loop:
				for {
					select {
					case <-updates:
						received = true
						break loop
					case <-deadline.C:
						break loop
					case <-ticker.C:
						if err := os.WriteFile(path, native.Content, 0600); err != nil {
							t.Error(err)
							break loop
						}
					}
				}
				ticker.Stop()
				deadline.Stop()
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatalf("watch ended: %v", err)
				}
				if !received {
					t.Fatalf("watch %d delivered no live update", attempt+1)
				}
			}
		})
	}
}

// Exercise the public Check contract for every CLI provider with the same real
// filesystem/process failures, so one provider cannot silently soften a broken install.
func TestCLIProviderCheckErrorTypes(t *testing.T) {
	providers := []spi.Provider{
		antigravitycli.NewProvider(), claudecode.NewProvider(), codexcli.NewProvider(), copilotcli.NewProvider(),
		cursorcli.NewProvider(), deepseektui.NewProvider(), droidcli.NewProvider(),
		geminicli.NewProvider(), grokbuild.NewProvider(), musecode.NewProvider(), piagent.NewProvider(), qwencode.NewProvider(),
	}
	for _, p := range providers {
		t.Run(p.Name(), func(t *testing.T) {
			for _, tt := range []struct {
				name, script, want string
				mode               os.FileMode
			}{
				{"missing", "", spi.CheckErrorNotFound, 0},
				{"permission denied", "#!/bin/sh\necho version\n", spi.CheckErrorPermissionDenied, 0600},
				{"failed probe", "#!/bin/sh\necho broken >&2\nexit 7\n", spi.CheckErrorUnknown, 0700},
				{"missing interpreter", "#!/nonexistent-specstory-test-interpreter\n", spi.CheckErrorUnknown, 0700},
				{"working", "#!/bin/sh\necho '1.2.3 (Claude Code)'\n", "", 0700},
			} {
				t.Run(tt.name, func(t *testing.T) {
					if runtime.GOOS == "windows" && tt.script != "" {
						t.Skip("POSIX executable fixture")
					}
					path := filepath.Join(t.TempDir(), "agent")
					if tt.script != "" {
						if err := os.WriteFile(path, []byte(tt.script), tt.mode); err != nil {
							t.Fatal(err)
						}
					}
					result := p.Check(`"` + path + `"`)
					if result.ErrorType != tt.want || result.Success != (tt.want == "") {
						t.Fatalf("Check = %+v, want ErrorType %q", result, tt.want)
					}
					if !result.Success && result.ErrorMessage == "" {
						t.Fatal("failed check has no explanation")
					}
				})
			}
		})
	}
}

func TestCLIProviderInvalidVersionOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	path := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		provider spi.Provider
		want     string
	}{
		{claudecode.NewProvider(), spi.CheckErrorUnexpectedOutput},
		{cursorcli.NewProvider(), spi.CheckErrorNoOutput},
		{codexcli.NewProvider(), spi.CheckErrorNoOutput},
	} {
		t.Run(tt.provider.Name(), func(t *testing.T) {
			result := tt.provider.Check(`"` + path + `"`)
			if result.Success || result.ErrorType != tt.want {
				t.Fatalf("empty version output = %+v, want %q", result, tt.want)
			}
		})
	}
}
