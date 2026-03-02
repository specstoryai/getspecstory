package spi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractShellPathHints(t *testing.T) {
	workspaceRoot := "/project"
	cwd := "/project/src"

	tests := []struct {
		name          string
		command       string
		cwd           string
		workspaceRoot string
		wantPaths     []string
	}{
		// --- Redirect operations ---
		{
			name:          "absolute path redirect under workspace",
			command:       `echo "text" > /project/file.txt`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"file.txt"},
		},
		{
			name:          "relative path redirect with ./",
			command:       `echo "text" > ./rel/file.txt`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/rel/file.txt"},
		},
		{
			name:          "bare filename redirect",
			command:       `echo "text" > file.txt`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/file.txt"},
		},
		{
			name:          "append redirect",
			command:       `echo "text" >> log.txt`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/log.txt"},
		},
		{
			name:          "stderr redirect",
			command:       `command 2> error.log`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/error.log"},
		},
		{
			name:          "combined redirect &>",
			command:       `command &> all.log`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/all.log"},
		},
		{
			name:          "no space before filename",
			command:       `command >file.txt`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/file.txt"},
		},
		{
			name:          "quoted redirect target",
			command:       `echo "text" > "path with spaces.txt"`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/path with spaces.txt"},
		},
		{
			name:          "parent directory redirect",
			command:       `echo "text" > ../other/file.txt`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"other/file.txt"},
		},

		// --- File-creating commands ---
		{
			name:          "touch single file",
			command:       `touch new_file.ts`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/new_file.ts"},
		},
		{
			name:          "touch multiple files",
			command:       `touch file1.txt file2.txt file3.go`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/file1.txt", "src/file2.txt", "src/file3.go"},
		},
		{
			name:          "mkdir -p",
			command:       `mkdir -p src/components/ui`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/src/components/ui"},
		},
		{
			name:          "cp destination",
			command:       `cp src/main.go backup/main.go`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/backup/main.go"},
		},
		{
			name:          "mv destination",
			command:       `mv old.go new.go`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/new.go"},
		},
		{
			name:          "tee target",
			command:       `echo "data" | tee output.txt`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/output.txt"},
		},
		{
			name:          "ln -s creates link",
			command:       `ln -s /target link_name`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/link_name"},
		},

		// --- Build output flags ---
		{
			name:          "go build -o",
			command:       `go build -o specstory`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/specstory"},
		},
		{
			name:          "go build -o with relative path",
			command:       `go build -o ./bin/specstory`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/bin/specstory"},
		},
		{
			name:          "gcc -o output",
			command:       `gcc -o output input.c`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/output"},
		},

		// --- Pipes with redirects ---
		{
			name:          "pipe to tee",
			command:       `grep pattern src/ | tee results.txt`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/results.txt"},
		},
		{
			name:          "pipe to redirect",
			command:       `cat input.txt | sort > sorted.txt`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/sorted.txt"},
		},

		// --- Command chaining ---
		{
			name:          "mkdir && cp",
			command:       `mkdir -p build && cp src/main.go build/main.go`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/build", "src/build/main.go"},
		},
		{
			name:          "touch ; touch",
			command:       `touch a.txt; touch b.txt`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/a.txt", "src/b.txt"},
		},
		{
			name:          "cd && redirect",
			command:       `cd /tmp && echo "hi" > out.txt`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			// cd is not tracked; redirect target resolved against original cwd
			wantPaths: []string{"src/out.txt"},
		},

		// --- CWD resolution ---
		{
			name:          "bare path with CWD resolves to workspace-relative",
			command:       `touch file.txt`,
			cwd:           "/project/src",
			workspaceRoot: "/project",
			wantPaths:     []string{"src/file.txt"},
		},
		{
			name:          "dotslash with CWD",
			command:       `touch ./file.txt`,
			cwd:           "/project/src",
			workspaceRoot: "/project",
			wantPaths:     []string{"src/file.txt"},
		},
		{
			name:          "parent dir with CWD",
			command:       `touch ../config.json`,
			cwd:           "/project/src",
			workspaceRoot: "/project",
			wantPaths:     []string{"config.json"},
		},

		// --- Workspace normalization ---
		{
			name:          "absolute path under workspace becomes relative",
			command:       `touch /project/src/main.go`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/main.go"},
		},

		// --- Edge cases: no false positives ---
		{
			name:          "empty command",
			command:       ``,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     nil,
		},
		{
			name:          "ls is read-only",
			command:       `ls -la`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     nil,
		},
		{
			name:          "echo without redirect",
			command:       `echo "hello world"`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     nil,
		},
		{
			name:          "cat is read-only",
			command:       `cat file.txt`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     nil,
		},
		{
			name:          "curl URL not extracted",
			command:       `curl https://example.com`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     nil,
		},
		{
			name:          "git commit no paths",
			command:       `git commit -m "fix bug"`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     nil,
		},
		{
			name:          "export no paths",
			command:       `export FOO=bar`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     nil,
		},
		{
			name:          "pwd no paths",
			command:       `pwd`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     nil,
		},

		// --- Multi-line ---
		{
			name:          "multi-line touch commands",
			command:       "touch a.txt\ntouch b.txt",
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/a.txt", "src/b.txt"},
		},

		// --- sed -i (in-place file modification) ---
		{
			name:          "sed -i modifies file in-place (macOS form)",
			command:       `sed -i '' 's/Sean/Thatcher/' /project/src/hello.py`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/hello.py"},
		},
		{
			name:          "sed -i modifies file in-place (Linux form)",
			command:       `sed -i 's/old/new/g' /project/src/config.yaml`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/config.yaml"},
		},
		{
			name:          "sed -i with -e expressions",
			command:       `sed -i -e 's/foo/bar/' -e 's/baz/qux/' /project/src/file.txt`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/file.txt"},
		},
		{
			name:          "sed without -i is read-only",
			command:       `sed 's/foo/bar/' file.txt`,
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     nil,
		},

		// --- Heredoc ---
		{
			name:          "heredoc with redirect captures redirect target",
			command:       "cat <<EOF > output.txt\ncontent line\nEOF",
			cwd:           cwd,
			workspaceRoot: workspaceRoot,
			wantPaths:     []string{"src/output.txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractShellPathHints(tt.command, tt.cwd, tt.workspaceRoot)
			if len(got) != len(tt.wantPaths) {
				t.Fatalf("got %d paths %v, want %d paths %v", len(got), got, len(tt.wantPaths), tt.wantPaths)
			}
			for i, want := range tt.wantPaths {
				if got[i] != want {
					t.Errorf("paths[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

func Test_expandTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "expands tilde prefix",
			path: "~/project/file.txt",
			want: filepath.Join(home, "project/file.txt"),
		},
		{
			name: "no tilde unchanged",
			path: "/absolute/path",
			want: "/absolute/path",
		},
		{
			name: "tilde without slash unchanged",
			path: "~user/file",
			want: "~user/file",
		},
		{
			name: "relative path unchanged",
			path: "relative/path",
			want: "relative/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandTilde(tt.path)
			if got != tt.want {
				t.Errorf("expandTilde(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		workspaceRoot string
		want          string
	}{
		{
			name:          "absolute under workspace becomes relative",
			path:          "/project/src/main.go",
			workspaceRoot: "/project",
			want:          "src/main.go",
		},
		{
			name:          "absolute outside workspace unchanged",
			path:          "/other/file.txt",
			workspaceRoot: "/project",
			want:          "/other/file.txt",
		},
		{
			name:          "relative path unchanged",
			path:          "src/main.go",
			workspaceRoot: "/project",
			want:          "src/main.go",
		},
		{
			name:          "empty workspace root returns path unchanged",
			path:          "/project/file.txt",
			workspaceRoot: "",
			want:          "/project/file.txt",
		},
		{
			name:          "workspace root itself",
			path:          "/project",
			workspaceRoot: "/project",
			want:          ".",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizePath(tt.path, tt.workspaceRoot)
			if got != tt.want {
				t.Errorf("NormalizePath(%q, %q) = %q, want %q", tt.path, tt.workspaceRoot, got, tt.want)
			}
		})
	}
}

func TestExtractShellPathHints_TildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}

	// Use home dir as workspace root so tilde-expanded paths get normalized
	workspaceRoot := filepath.Join(home, "project")
	cwd := workspaceRoot

	got := ExtractShellPathHints("touch ~/project/file.txt", cwd, workspaceRoot)
	if len(got) != 1 {
		t.Fatalf("got %d paths %v, want 1", len(got), got)
	}
	if got[0] != "file.txt" {
		t.Errorf("got %q, want %q", got[0], "file.txt")
	}
}
