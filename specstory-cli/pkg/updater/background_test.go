package updater

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestBackgroundStartupPolicy(t *testing.T) {
	for _, scenario := range []string{"disabled", "environment", "CI", "development", "manual", "inspection fails", "paused", "cached", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("CI", "")
			t.Setenv("SPECSTORY_NO_AUTO_UPDATE", "")
			m := &Manager{Version: "1.0.0", Kind: "native", statePath: filepath.Join(t.TempDir(), "status.json")}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			version := "1.0.0"
			switch scenario {
			case "environment":
				t.Setenv("SPECSTORY_NO_AUTO_UPDATE", "1")
			case "CI":
				t.Setenv("CI", "true")
			case "development":
				version = "dev"
			case "manual":
				m.Kind = "manual"
			case "paused":
				must(t, m.save(Status{Paused: true}))
			case "cached":
				must(t, m.save(Status{CheckedAt: time.Now()}))
			case "canceled":
				cancel()
			}
			startBackground(ctx, version, scenario == "disabled", func(string) (*Manager, error) {
				if scenario == "inspection fails" {
					return nil, errors.New("executable unavailable")
				}
				return m, nil
			}, func(*Manager) error {
				t.Error("ineligible startup launched an updater")
				return nil
			})
		})
	}
}

func TestBackgroundSchedulingAndCancellation(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("SPECSTORY_NO_AUTO_UPDATE", "")
	for _, failSpawn := range []bool{false, true} {
		t.Run(map[bool]string{false: "successful worker", true: "spawn failure"}[failSpawn], func(t *testing.T) {
			root := t.TempDir()
			// Go's fake clock exercises the real six-hour timer without sleeping
			// in wall time. Wait synchronizes assertions with the scheduler.
			synctest.Test(t, func(t *testing.T) {
				m := &Manager{Version: "1.0.0", Kind: "native", statePath: filepath.Join(root, "status.json")}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var calls atomic.Int32
				startBackground(ctx, "1.0.0", false, func(string) (*Manager, error) { return m, nil }, func(*Manager) error {
					calls.Add(1)
					if failSpawn {
						return errors.New("cannot launch worker")
					}
					return m.save(Status{CheckedAt: time.Now()})
				})
				synctest.Wait()
				if calls.Load() != 1 {
					t.Fatalf("initial launches: %d", calls.Load())
				}
				time.Sleep(checkInterval - time.Second)
				synctest.Wait()
				if calls.Load() != 1 {
					t.Fatal("launched before the cache interval")
				}
				time.Sleep(time.Second)
				synctest.Wait()
				if calls.Load() != 2 {
					t.Fatalf("did not retry after the interval: %d", calls.Load())
				}
				cancel()
				synctest.Wait()
				time.Sleep(2 * checkInterval)
				synctest.Wait()
				if calls.Load() != 2 {
					t.Fatal("canceled scheduler kept launching workers")
				}
			})
		})
	}
}
