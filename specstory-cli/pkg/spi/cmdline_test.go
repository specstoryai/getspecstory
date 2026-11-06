package spi

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestAgentWorkingDir(t *testing.T) {
	launchDir := t.TempDir()
	project := t.TempDir()
	workspaceFile := filepath.Join(project, "my.code-workspace")
	if err := os.WriteFile(workspaceFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		projectPath string
		want        string
	}{
		{"another local project", project, project},
		{"no project path", "", launchDir},
		{"missing local path", filepath.Join(project, "not-here"), launchDir},
		{"code-workspace file", workspaceFile, launchDir},
		// Rooted without a drive on Windows; a relative path elsewhere.
		{"driveless path to a real directory", project[len(filepath.VolumeName(project)):][1:], launchDir},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AgentWorkingDir(tt.projectPath, launchDir); got != tt.want {
				t.Errorf("AgentWorkingDir(%q) = %q, want %q", tt.projectPath, got, tt.want)
			}
		})
	}
	if runtime.GOOS == "windows" {
		driveless := project[len(filepath.VolumeName(project)):]
		if got := AgentWorkingDir(driveless, launchDir); got != launchDir {
			t.Errorf("AgentWorkingDir(%q) = %q, want the launch directory", driveless, got)
		}
	}
}

// A watcher must follow the agent: into a local project, and to the launch
// directory when the project path is one no process can run in.
func TestRunningAgentDir(t *testing.T) {
	project := t.TempDir()
	t.Chdir(t.TempDir())
	launchDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	launchDir = CanonicalizePathOrClean(launchDir)

	if got := RunningAgentDir(project); got != project {
		t.Errorf("local project: got %q, want %q", got, project)
	}
	for _, projectPath := range []string{"", filepath.Join(project, "not-here"), "/no/such/remote/project"} {
		if got := RunningAgentDir(projectPath); got != launchDir {
			t.Errorf("RunningAgentDir(%q) = %q, want the launch directory %q", projectPath, got, launchDir)
		}
	}
}

func TestSplitCommandLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "simple command",
			input: "claude",
			want:  []string{"claude"},
		},
		{
			name:  "command with simple arguments",
			input: "claude --debug --verbose",
			want:  []string{"claude", "--debug", "--verbose"},
		},
		{
			name:  "double quoted argument with spaces",
			input: `claude --config "~/my settings/config.json"`,
			want:  []string{"claude", "--config", "~/my settings/config.json"},
		},
		{
			name:  "single quoted argument with spaces",
			input: `claude --log-file '/path with spaces/log.txt'`,
			want:  []string{"claude", "--log-file", "/path with spaces/log.txt"},
		},
		{
			name:  "mixed quotes",
			input: `claude --arg1 "value 1" --arg2 'value 2'`,
			want:  []string{"claude", "--arg1", "value 1", "--arg2", "value 2"},
		},
		{
			name:  "escaped quotes inside double quotes",
			input: `claude --msg "He said \"hello\""`,
			want:  []string{"claude", "--msg", `He said "hello"`},
		},
		{
			name:  "escaped quotes inside single quotes",
			input: `claude --msg 'It'\''s working'`,
			want:  []string{"claude", "--msg", "It's working"},
		},
		{
			name:  "escaped backslash",
			input: `claude --path "C:\\Users\\test"`,
			want:  []string{"claude", "--path", `C:\Users\test`},
		},
		{
			name:  "multiple spaces between arguments",
			input: "claude   --debug    --verbose",
			want:  []string{"claude", "--debug", "--verbose"},
		},
		{
			name:  "leading and trailing spaces",
			input: "  claude --debug  ",
			want:  []string{"claude", "--debug"},
		},
		{
			name:  "tabs and newlines",
			input: "claude\t--debug\n--verbose",
			want:  []string{"claude", "--debug", "--verbose"},
		},
		{
			name:  "empty string",
			input: "",
			want:  []string{},
		},
		{
			name:  "only whitespace",
			input: "   \t\n  ",
			want:  []string{},
		},
		{
			name:  "empty quotes",
			input: `claude --arg ""`,
			want:  []string{"claude", "--arg"},
		},
		{
			name:  "quoted empty string in middle",
			input: `claude "" --debug`,
			want:  []string{"claude", "--debug"},
		},
		{
			name:  "unclosed quote (treated as literal to end)",
			input: `claude --arg "value`,
			want:  []string{"claude", "--arg", "value"},
		},
		{
			name:  "complex real-world example",
			input: `claude --config "~/Library/Application Support/claude/config.json" --log-level debug --output-dir "/tmp/my logs"`,
			want:  []string{"claude", "--config", "~/Library/Application Support/claude/config.json", "--log-level", "debug", "--output-dir", "/tmp/my logs"},
		},
		{
			name:  "argument with equals and quotes",
			input: `claude --setting="value with spaces"`,
			want:  []string{"claude", `--setting=value with spaces`},
		},
		{
			name:  "codex command example",
			input: "codex",
			want:  []string{"codex"},
		},
		{
			name:  "cursor command example",
			input: "cursor-agent",
			want:  []string{"cursor-agent"},
		},
		{
			name:  "command with path",
			input: "/usr/local/bin/claude --debug",
			want:  []string{"/usr/local/bin/claude", "--debug"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitCommandLine(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("SplitCommandLine() returned %d args, want %d\nGot:  %#v\nWant: %#v", len(got), len(tt.want), got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("SplitCommandLine() arg[%d] = %q, want %q\nGot:  %#v\nWant: %#v", i, got[i], tt.want[i], got, tt.want)
				}
			}
		})
	}
}

// TestSplitCommandLine_EdgeCases tests additional edge cases
func TestSplitCommandLine_EdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "consecutive empty quotes",
			input: `cmd "" "" --arg`,
			want:  []string{"cmd", "--arg"},
		},
		{
			name:  "quote after non-whitespace",
			input: `cmd arg"with quote"`,
			want:  []string{"cmd", "argwith quote"},
		},
		{
			name:  "backslash at end",
			input: `cmd arg\`,
			want:  []string{"cmd", "arg"},
		},
		{
			name:  "only escaped spaces",
			input: `cmd\ with\ spaces`,
			want:  []string{"cmd with spaces"},
		},
		{
			name:  "mixed escaped and quoted",
			input: `cmd "arg 1" arg\ 2 'arg 3'`,
			want:  []string{"cmd", "arg 1", "arg 2", "arg 3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitCommandLine(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("SplitCommandLine() returned %d args, want %d\nGot:  %#v\nWant: %#v", len(got), len(tt.want), got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("SplitCommandLine() arg[%d] = %q, want %q\nGot:  %#v\nWant: %#v", i, got[i], tt.want[i], got, tt.want)
				}
			}
		})
	}
}

// TestSplitCommandLine_Benchmarks provides benchmark data
func BenchmarkSplitCommandLine_Simple(b *testing.B) {
	input := "claude --debug --verbose"
	for i := 0; i < b.N; i++ {
		SplitCommandLine(input)
	}
}

func BenchmarkSplitCommandLine_Complex(b *testing.B) {
	input := `claude --config "~/Library/Application Support/claude/config.json" --log-level debug --output-dir "/tmp/my logs"`
	for i := 0; i < b.N; i++ {
		SplitCommandLine(input)
	}
}

func BenchmarkSplitCommandLine_LongCommand(b *testing.B) {
	// Simulate a very long command with many arguments
	var parts []string
	for i := 0; i < 100; i++ {
		parts = append(parts, "--arg", `"value with spaces"`)
	}
	input := "cmd " + strings.Join(parts, " ")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SplitCommandLine(input)
	}
}

func TestEnsureResumeArgs(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		sessionID string
		expected  []string
	}{
		{
			name:      "no session id leaves args alone",
			args:      []string{"--model", "x"},
			sessionID: "",
			expected:  []string{"--model", "x"},
		},
		{
			name:      "resume is a subcommand, not a flag",
			args:      nil,
			sessionID: "abc-123",
			expected:  []string{"resume", "abc-123"},
		},
		{
			name:      "appended after existing flags",
			args:      []string{"--model", "muse-spark-1.2"},
			sessionID: "abc-123",
			expected:  []string{"--model", "muse-spark-1.2", "resume", "abc-123"},
		},
		{
			name:      "bare resume subcommand gets the id",
			args:      []string{"resume"},
			sessionID: "abc-123",
			expected:  []string{"resume", "abc-123"},
		},
		{
			name:      "resume followed by a flag gets the id inserted",
			args:      []string{"resume", "--verbose"},
			sessionID: "abc-123",
			expected:  []string{"resume", "abc-123", "--verbose"},
		},
		{
			// A pinned id in the configured command is a default; the requested
			// one is this run's instruction and has to win, or the wrong
			// conversation opens without a word.
			name:      "requested session replaces one pinned in the command",
			args:      []string{"resume", "existing-id"},
			sessionID: "abc-123",
			expected:  []string{"resume", "abc-123"},
		},
		{
			name:      "pinned id is replaced without disturbing surrounding args",
			args:      []string{"--model", "muse-spark-1.2", "resume", "existing-id", "--verbose"},
			sessionID: "abc-123",
			expected:  []string{"--model", "muse-spark-1.2", "resume", "abc-123", "--verbose"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := slices.Clone(tt.args)
			got := EnsureResumeArgs(tt.args, "resume", tt.sessionID)

			if !slices.Equal(got, tt.expected) {
				t.Errorf("EnsureResumeArgs(%v, %q) = %v, want %v", tt.args, tt.sessionID, got, tt.expected)
			}
			// The caller's slice must survive: providers hand in a sub-slice of
			// their parsed command line.
			if !slices.Equal(tt.args, input) {
				t.Errorf("input args mutated: %v, want %v", tt.args, input)
			}
		})
	}
}

func TestEnsureResumeFlagArgs(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		id   string
		want []string
	}{
		{"disabled", []string{"--resume", "old"}, "", []string{"--resume", "old"}},
		{"append", []string{"--verbose"}, "new", []string{"--verbose", "--resume", "new"}},
		{"pinned", []string{"--resume", "old"}, "new", []string{"--resume", "new"}},
		{"bare", []string{"-r", "--verbose"}, "new", []string{"-r", "new", "--verbose"}},
		{"empty", []string{"--resume", ""}, "new", []string{"--resume", "new"}},
		{"equals", []string{"--resume=old", "-r="}, "new", []string{"--resume=new", "-r=new"}},
		{"repeated", []string{"-r", "old", "--resume", "other"}, "new", []string{"-r", "new", "--resume", "new"}},
		{"positional", []string{"--", "--resume", "prompt"}, "new", []string{"--resume", "new", "--", "--resume", "prompt"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			backing := make([]string, len(tt.args)+4)
			copy(backing, tt.args)
			before := slices.Clone(backing)
			got := EnsureResumeFlagArgs(backing[:len(tt.args)], tt.id, "--resume", "-r")
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			if !slices.Equal(backing, before) {
				t.Fatal("mutated caller's backing array")
			}
		})
	}
}

// A child moved to the project directory must still find what the launch
// directory's relative paths named.
func TestRunInProject(t *testing.T) {
	launchDir := t.TempDir()
	project := t.TempDir()
	t.Chdir(launchDir)
	// Resolve through the OS: the temp dir may be reached through a symlink.
	launchDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_RELATIVE_HOME", filepath.Join("state", "agent"))
	t.Setenv("TEST_ABSOLUTE_HOME", filepath.Join(launchDir, "abs"))
	t.Setenv("TEST_TILDE_HOME", "~/agent")
	t.Setenv("TEST_EMPTY_HOME", "")

	relative := "." + string(filepath.Separator) + filepath.Join("bin", "agent")
	cmd := exec.Command(relative)
	RunInProject(cmd, project, "TEST_RELATIVE_HOME", "TEST_ABSOLUTE_HOME", "TEST_TILDE_HOME", "TEST_EMPTY_HOME")

	if cmd.Dir != project {
		t.Errorf("Dir = %q, want %q", cmd.Dir, project)
	}
	if want := filepath.Join(launchDir, "bin", "agent"); !strings.HasPrefix(cmd.Path, want) {
		t.Errorf("Path = %q, want it under the launch directory: %q", cmd.Path, want)
	}
	wantRelativeHome := "TEST_RELATIVE_HOME=" + filepath.Join(launchDir, "state", "agent")
	if !slices.Contains(cmd.Env, wantRelativeHome) {
		t.Errorf("Env lacks %q", wantRelativeHome)
	}
	for _, unchanged := range []string{"TEST_ABSOLUTE_HOME", "TEST_TILDE_HOME", "TEST_EMPTY_HOME"} {
		count := 0
		for _, entry := range cmd.Env {
			if strings.HasPrefix(entry, unchanged+"=") {
				count++
			}
		}
		if count > 1 {
			t.Errorf("%s was rewritten: %d entries", unchanged, count)
		}
	}

	// Remote workspaces and .code-workspace files are valid --project-path
	// values, but no process can run in them.
	workspaceFile := filepath.Join(launchDir, "my.code-workspace")
	if err := os.WriteFile(workspaceFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"empty":                "",
		"missing local path":   filepath.Join(launchDir, "not-here"),
		"code-workspace file":  workspaceFile,
		"relative remote path": filepath.Join("home", "user", "project"),
	} {
		t.Run(name, func(t *testing.T) {
			stay := exec.Command(relative)
			RunInProject(stay, path, "TEST_RELATIVE_HOME")
			if stay.Dir != "" {
				t.Errorf("Dir = %q, want the launch directory", stay.Dir)
			}
			if stay.Path != relative || stay.Env != nil {
				t.Errorf("Path = %q, Env set = %v; want both untouched", stay.Path, stay.Env != nil)
			}
		})
	}

	// A bare name is a PATH lookup, not a path under the launch directory.
	bare := exec.Command("specstory-no-such-agent")
	RunInProject(bare, project)
	if bare.Path != "specstory-no-such-agent" {
		t.Errorf("bare command Path = %q, want it untouched", bare.Path)
	}
}
