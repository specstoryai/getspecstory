package spi

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestClassifyCheckError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "binary missing from PATH",
			err:  &exec.Error{Name: "agy", Err: exec.ErrNotFound},
			want: CheckErrorNotFound,
		},
		{
			name: "permission denied via os.ErrPermission",
			err:  os.ErrPermission,
			want: CheckErrorPermissionDenied,
		},
		{
			name: "wrapped permission denied",
			err:  &os.PathError{Op: "exec", Path: "/x", Err: os.ErrPermission},
			want: CheckErrorPermissionDenied,
		},
		{
			name: "generic error is unclassified",
			err:  errors.New("the binary crashed"),
			want: CheckErrorUnknown,
		},
		{
			name: "unrelated sentinel is unclassified",
			err:  os.ErrInvalid,
			want: CheckErrorUnknown,
		},
		{
			name: "nil error classifies as empty",
			err:  nil,
			want: "",
		},
		{
			name: "wrapped missing file",
			err:  &os.PathError{Op: "exec", Path: "/x", Err: os.ErrNotExist},
			want: CheckErrorNotFound,
		},
		{
			// A PathError that is neither missing nor permission must not swallow
			// the plain os.ErrPermission check that follows it.
			name: "unrelated PathError wrapping a permission error still resolves",
			err:  fmt.Errorf("probe: %w", &os.PathError{Op: "exec", Path: "/x", Err: os.ErrPermission}),
			want: CheckErrorPermissionDenied,
		},
		{
			name: "unrelated PathError is unclassified",
			err:  &os.PathError{Op: "exec", Path: "/x", Err: errors.New("i/o error")},
			want: CheckErrorUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyCheckError(tt.err)
			if got != tt.want {
				t.Errorf("ClassifyCheckError(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestCheckLookupAndExecutionClassification(t *testing.T) {
	if got := ClassifyCheckError(fmt.Errorf("storage: %w", os.ErrNotExist)); got != CheckErrorNotFound {
		t.Fatalf("wrapped missing storage = %q", got)
	}
	if got := ClassifyCheckError(fmt.Errorf("lookup: %w", exec.ErrNotFound)); got != CheckErrorNotFound {
		t.Fatalf("wrapped missing executable = %q", got)
	}
	if got := ClassifyCheckExecutionError(&os.PathError{Op: "fork/exec", Path: "/agent", Err: os.ErrNotExist}); got != CheckErrorUnknown {
		t.Fatalf("missing interpreter = %q, want unknown", got)
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix PATH permission semantics")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	path := filepath.Join(dir, "agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LookPathForCheck("agent")
	if got := ClassifyCheckError(err); got != CheckErrorPermissionDenied {
		t.Fatalf("non-executable PATH candidate = %q (%v), want permission_denied", got, err)
	}
	// The denied candidate must not mask a working installation later on PATH.
	runnableDir := t.TempDir()
	runnable := filepath.Join(runnableDir, "agent")
	if err := os.WriteFile(runnable, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+runnableDir)
	got, err := LookPathForCheck("agent")
	if err != nil || got != runnable {
		t.Fatalf("lookup = %q, %v; want %q", got, err, runnable)
	}
}
