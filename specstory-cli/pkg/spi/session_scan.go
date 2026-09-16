package spi

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// ScanSessionsInParallel walks root for *.jsonl session files and scans each one concurrently
// with scan, returning the collected refs. It is the shared engine behind each provider's
// ListAllAgentChatSessionsProgress: providers differ only in where their sessions live (root)
// and how a single file's header maps to a GlobalSessionRef (scan).
//
// scan returns (ref, nil) to include a session, (nil, nil) to skip a non-session file
// (warmup-only / empty / sidechain-only), or (nil, err) to log-and-skip a malformed file.
// Files are independent and the per-file read+parse is CPU-bound, so scanning fans out across
// CPUs; output order is unspecified (reindex dedups and sorts later). r counts sessions found
// (not files scanned) and is nil-safe. label names the provider in scan-failure logs.
func ScanSessionsInParallel(root, label string, r *ScanReporter, scan func(path string) (*GlobalSessionRef, error)) ([]GlobalSessionRef, error) {
	// Phase 1: collect file paths (dirents only — no opens).
	var paths []string
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries rather than abort the whole sweep
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walking %s sessions: %w", label, walkErr)
	}

	// Phase 2: scan headers in parallel. min(NumCPU, 12) is always ≥ 1, so no lower-bound guard.
	workers := min(runtime.NumCPU(), 12)
	pathCh := make(chan string, workers)
	refs := make([]GlobalSessionRef, 0, len(paths))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Go(func() {
			for path := range pathCh {
				ref, scanErr := safeScan(scan, path)
				if scanErr != nil {
					slog.Warn("reindex: failed to scan session", "agent", label, "path", path, "error", scanErr)
					continue
				}
				if ref == nil {
					continue // not a session (warmup / empty / sidechain)
				}
				mu.Lock()
				refs = append(refs, *ref)
				mu.Unlock()
				r.Add(1) // count sessions found, not files scanned
			}
		})
	}
	for _, path := range paths {
		pathCh <- path
	}
	close(pathCh)
	wg.Wait()
	return refs, nil
}

// safeScan runs scan(path), converting a panic into an error so one malformed session file is
// logged and skipped (via the caller's normal error path) rather than crashing the whole sweep.
func safeScan(scan func(string) (*GlobalSessionRef, error), path string) (ref *GlobalSessionRef, err error) {
	defer func() {
		if r := recover(); r != nil {
			ref, err = nil, fmt.Errorf("panic scanning session: %v", r)
		}
	}()
	return scan(path)
}
