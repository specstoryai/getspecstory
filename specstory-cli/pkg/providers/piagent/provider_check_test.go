package piagent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheck_CustomCommandArgsPassedToVersionProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is POSIX-only")
	}

	tmp := t.TempDir()
	script := filepath.Join(tmp, "pi-wrapper.sh")
	content := "#!/bin/sh\n" +
		"if [ \"$1\" != \"--ok\" ]; then\n" +
		"  echo \"missing required launcher arg\" >&2\n" +
		"  exit 2\n" +
		"fi\n" +
		"if [ \"$2\" != \"--version\" ]; then\n" +
		"  echo \"missing version flag\" >&2\n" +
		"  exit 3\n" +
		"fi\n" +
		"echo \"pi 1.2.3\"\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	p := NewProvider()
	res := p.Check(script + " --ok")
	if !res.Success {
		t.Fatalf("Check failed: %s", res.ErrorMessage)
	}
	if res.Version != "pi 1.2.3" {
		t.Fatalf("version = %q, want %q", res.Version, "pi 1.2.3")
	}
}

func TestCheckVersionStreamsAndFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixtures")
	}
	for _, tt := range []struct {
		name, script, version string
		success               bool
	}{
		{"stderr", "echo 0.85.1 >&2", "0.85.1", true},
		{"stdout preferred", "echo 0.85.1; echo diagnostic >&2", "0.85.1", true},
		{"empty", "exit 0", "unknown", true},
		{"failure", "echo failure >&2; exit 7", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pi wrapper")
			if err := os.WriteFile(path, []byte("#!/bin/sh\n"+tt.script+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			command := `"` + path + `" --custom`
			result := NewProvider().Check(command)
			if result.Success != tt.success || result.Version != tt.version {
				t.Fatalf("result=%+v", result)
			}
			if !tt.success && (!strings.Contains(result.ErrorMessage, command+" --version") || !strings.Contains(result.ErrorMessage, "failure")) {
				t.Fatalf("lost command or stderr: %s", result.ErrorMessage)
			}
		})
	}
}
