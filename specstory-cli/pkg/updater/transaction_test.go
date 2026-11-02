package updater

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFailedRotationPreservesPreviousBackup(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("SPECSTORY_NO_AUTO_UPDATE", "")
	for _, failStatus := range []bool{false, true} {
		t.Run(map[bool]string{false: "status writable", true: "status fails too"}[failStatus], func(t *testing.T) {
			m, f := fixture(t, "linux", "amd64", []byte("1.0.0"), []byte("2.0.0"))
			_, err := m.Run(t.Context(), false, false)
			must(t, err)
			previous, err := m.previousPath(m.Status())
			must(t, err)
			m.Version, f.version = "2.0.0", "3.0.0"
			f.archive = archiveFor(t, "linux", []byte("3.0.0"), "specstory")
			f.manifest = digest(f.archive) + "  SpecStoryCLI_Linux_x86_64.tar.gz\n"
			failure := errors.New("target rename denied")
			m.apply = func(_ []byte, backup string) error {
				if backup == previous {
					t.Fatal("library would remove the existing rollback copy")
				}
				// Reproduce the library's destructive first step, then a failed rename.
				must(t, os.Remove(backup))
				return failure
			}
			if failStatus {
				failFinalStatusWrites(m)
			}
			_, err = m.Run(t.Context(), false, false)
			if !errors.Is(err, failure) {
				t.Fatalf("missing rotation error: %v", err)
			}
			if string(contents(t, m.Executable)) != "2.0.0" || string(contents(t, previous)) != "1.0.0" {
				t.Fatal("failed rotation lost the current executable or prior rollback")
			}
			if !failStatus && !m.Due(time.Now()) {
				t.Fatal("failed replacement was throttled instead of retryable on the next launch")
			}
			m.persist, m.apply = nil, nil
			_, err = m.Rollback(t.Context())
			must(t, err)
			if string(contents(t, m.Executable)) != "1.0.0" {
				t.Fatal("preserved backup was not usable for rollback")
			}
		})
	}
}

func TestReadOnlyCheckAllowsDevelopmentBuilds(t *testing.T) {
	for _, version := range []string{"dev", "2.0.0-rc.1"} {
		m, f := fixture(t, "linux", "amd64", []byte("1.0.0"), []byte("2.0.0"))
		m.Version = version
		status, err := m.Run(t.Context(), true, false)
		must(t, err)
		if status.Latest != "2.0.0" || len(f.requests) != 1 || string(contents(t, m.Executable)) != "1.0.0" {
			t.Fatalf("read-only lookup failed for %s: %+v", version, status)
		}
		if _, err := os.Stat(m.statePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("read-only development check wrote status")
		}
	}
}

// Keep the pre-replacement intent on disk, simulating disk/permission failure
// for every later write (including Run's deferred diagnostic status save).
func failFinalStatusWrites(m *Manager) {
	writes := 0
	m.persist = func(status Status) error {
		writes++
		if writes > 1 {
			return errors.New("status filesystem unavailable")
		}
		return m.writeStatus(status)
	}
}

func TestUpdateRecoversAfterFinalStatusFailure(t *testing.T) {
	m, _ := fixture(t, "linux", "amd64", []byte("1.0.0"), []byte("2.0.0"))
	failFinalStatusWrites(m)
	_, err := m.Run(t.Context(), false, false)
	if err == nil || !strings.Contains(err.Error(), "status could not be finalized") {
		t.Fatalf("missing status failure: %v", err)
	}
	m.persist, m.Version = nil, "2.0.0"
	status := m.Status()
	if status.Installed != "2.0.0" || status.Previous != "1.0.0" || status.Pending != nil {
		t.Fatalf("lost recovery metadata: %+v", status)
	}
	_, err = m.Rollback(t.Context())
	must(t, err)
	if string(contents(t, m.Executable)) != "1.0.0" {
		t.Fatal("could not roll back after status write failure")
	}
}

func TestRollbackPauseSurvivesFinalStatusFailure(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("SPECSTORY_NO_AUTO_UPDATE", "")
	m, f := fixture(t, "linux", "amd64", []byte("1.0.0"), []byte("2.0.0"))
	_, err := m.Run(t.Context(), false, false)
	must(t, err)
	m.Version = "2.0.0"
	failFinalStatusWrites(m)
	_, err = m.Rollback(t.Context())
	if err == nil {
		t.Fatal("expected final status failure")
	}
	m.persist, m.Version = nil, "1.0.0"
	if !m.Status().Paused || m.Due(time.Now().Add(2*checkInterval)) {
		t.Fatal("lost the rollback pause after status write failure")
	}
	requests := len(f.requests)
	_, err = m.Run(t.Context(), false, true)
	must(t, err)
	if len(f.requests) != requests || string(contents(t, m.Executable)) != "1.0.0" {
		t.Fatal("automatic update undid the rollback")
	}
	_, err = m.Run(t.Context(), false, false)
	must(t, err)
	if m.Status().Paused || string(contents(t, m.Executable)) != "2.0.0" {
		t.Fatal("explicit update did not resume after recovery")
	}
}

func TestRecoveryMetadataFailurePreventsReplacement(t *testing.T) {
	m, _ := fixture(t, "linux", "amd64", []byte("1.0.0"), []byte("2.0.0"))
	m.persist = func(Status) error { return os.ErrPermission }
	m.apply = func([]byte, string) error {
		t.Fatal("replaced binary without saving recovery metadata")
		return nil
	}
	_, err := m.Run(t.Context(), false, false)
	if !errors.Is(err, os.ErrPermission) || string(contents(t, m.Executable)) != "1.0.0" {
		t.Fatalf("unsafe replacement: %v", err)
	}
}

func TestRollbackRejectsUnownedBackupPath(t *testing.T) {
	m, _ := fixture(t, "linux", "amd64", []byte("1.0.0"), []byte("2.0.0"))
	for _, name := range []string{"../outside", `..\outside`, "unrelated"} {
		must(t, m.save(Status{PreviousFile: name, Previous: "0.9.0"}))
		if _, err := m.Rollback(t.Context()); err == nil {
			t.Fatalf("accepted backup outside updater ownership: %q", name)
		}
	}
}
