package grokbuild

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFactoryCredentialIsFileOnlyAndRemoved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("factory installer supports macOS and Linux")
	}
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("factory prerequisite unavailable: %s", tool)
		}
	}
	script, err := filepath.Abs("factory/list-tools")
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range []string{"success", "failure"} {
		t.Run(outcome, func(t *testing.T) {
			home := t.TempDir()
			bin := filepath.Join(home, ".local", "bin")
			if err := os.MkdirAll(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			// Only a fake credential reaches this fixture. The child checks that the
			// real script exposes it through its private file, never duplicate JSON.
			child := `#!/bin/sh
[ -z "${GROK_AUTH_JSON+x}" ] || exit 71
[ "$(cat "$GROK_AUTH_PATH")" = '{"qa_only":"credential-fixture"}' ] || exit 72
[ "$GROK_QA_FACTORY_OUTCOME" != failure ] || exit 73
printf '%s\n' '{"type":"available_commands","tools":["write","read_file"]}'
`
			if err := os.WriteFile(filepath.Join(bin, "grok"), []byte(child), 0o700); err != nil {
				t.Fatal(err)
			}
			// The stub exits immediately; avoid requiring GNU timeout on developer Macs.
			if err := os.WriteFile(filepath.Join(bin, "timeout"), []byte("#!/bin/sh\nshift\nexec \"$@\"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("GROK_AUTH_JSON", `{"qa_only":"credential-fixture"}`)
			t.Setenv("GROK_QA_FACTORY_OUTCOME", outcome)
			cmd := exec.Command("bash", script)
			cmd.Dir = t.TempDir()
			output, err := cmd.Output()
			if outcome == "success" && (err != nil || string(output) != "read_file\nwrite\n") {
				t.Fatalf("factory declaration failed: %q, %v", output, err)
			}
			if outcome == "failure" && (err == nil || len(output) != 0) {
				t.Fatalf("factory failure accepted: %q, %v", output, err)
			}
			files, err := filepath.Glob(filepath.Join(home, ".grok", "factory-auth.*"))
			if err != nil || len(files) != 0 {
				t.Fatalf("temporary credential remains: %v, %v", files, err)
			}
			if strings.Contains(string(output), "credential-fixture") {
				t.Fatal("credential leaked to stdout")
			}
		})
	}
}
