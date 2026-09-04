package piagent

import (
	"os"
	"path/filepath"
	"runtime"
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
