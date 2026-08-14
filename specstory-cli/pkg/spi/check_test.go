package spi

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
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
