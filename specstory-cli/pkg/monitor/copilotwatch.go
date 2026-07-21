//go:build copilotide_monitor

package monitor

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/copilotide"
)

// copilotWorkspaceJSONRetries and copilotWorkspaceJSONRetryDelay bound the
// wait for workspace.json inside a freshly created workspace directory: VS
// Code creates the directory first and writes workspace.json asynchronously,
// so an immediate read races that write and would permanently miss the
// workspace. ~2s total is generous for a local write. The delay is a var so
// tests can shrink it instead of sleeping for real.
const copilotWorkspaceJSONRetries = 10

var copilotWorkspaceJSONRetryDelay = 200 * time.Millisecond

// WatchCopilotWorkspaceStorage watches one VS Code variant's workspaceStorage
// directory for Copilot chat activity and reports the owning discovered repo
// via onActivity, until ctx is cancelled.
//
// This deliberately does NOT reuse WatchRootForNewEntries: workspaceStorage
// commonly holds thousands of per-workspace directories and fsnotify costs a
// file descriptor per watched directory (kqueue on macOS), so watching the
// whole tree would exhaust descriptors. Instead:
//
//   - workspaceStorage is enumerated ONCE at startup; each workspace.json is
//     resolved to its project folder(s) and only workspaces mapping into the
//     discovered-repo set are kept (typically tens of matches), and
//   - fsnotify watches only those workspaces' chatSessions directories, plus
//     the workspaceStorage root itself (a single watch) so workspace
//     directories created while the monitor runs are picked up too.
//
// A pre-existing matched workspace with no chatSessions directory yet is
// skipped at startup (fsnotify cannot watch a missing path); its first-ever
// chat is picked up on the next monitor run. Workspace directories created at
// runtime DO get chatSessions-creation tracking — via a temporary watch on
// the workspace directory itself — because at creation time chatSessions
// never exists yet and the root watch alone would never see it appear.
func WatchCopilotWorkspaceStorage(ctx context.Context, storageRoot string, resolver *Resolver, onActivity func(repo string)) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() {
		_ = watcher.Close() // Best-effort cleanup; nothing to do if closing fails.
	}()

	root := filepath.Clean(storageRoot)
	if err := watcher.Add(root); err != nil {
		return err
	}

	// chatDirToRepo maps each watched chatSessions directory to its repo;
	// any Create/Write inside those directories is chat activity for that repo.
	chatDirToRepo, err := mapCopilotWorkspaces(root, resolver)
	if err != nil {
		return err
	}
	for chatDir := range chatDirToRepo {
		if addErr := watcher.Add(chatDir); addErr != nil {
			// One unwatchable directory must not take down the variant's whole
			// watch; drop the mapping so events can't be misattributed later.
			slog.Warn("Monitor: failed to watch Copilot chatSessions dir", "path", chatDir, "error", addErr)
			delete(chatDirToRepo, chatDir)
		}
	}
	slog.Info("Monitor: watching Copilot workspace storage", "root", root, "matchedWorkspaces", len(chatDirToRepo))

	// pendingWorkspaces maps runtime-created, repo-matched workspace
	// directories — watched while their chatSessions dir does not exist yet —
	// to their repo.
	pendingWorkspaces := make(map[string]string)

	// resolvedWorkspace carries a repo resolution for a runtime-created
	// workspace directory back into the event loop, so the retrying (sleeping)
	// workspace.json read never blocks event processing.
	type resolvedWorkspace struct {
		wsDir string
		repo  string
		ok    bool
	}
	newWorkspaces := make(chan resolvedWorkspace)
	// resolving de-dupes concurrent resolution attempts for the same new
	// directory (fsnotify can deliver more than one event for a creation).
	resolving := make(map[string]bool)

	for {
		select {
		case <-ctx.Done():
			// Cancellation is the normal way to stop watching, not an error.
			return nil

		case ws := <-newWorkspaces:
			delete(resolving, ws.wsDir)
			if !ws.ok {
				continue
			}
			chatDir := copilotide.GetChatSessionsPath(ws.wsDir)
			if _, statErr := os.Stat(chatDir); statErr == nil {
				if addErr := watcher.Add(chatDir); addErr != nil {
					slog.Warn("Monitor: failed to watch Copilot chatSessions dir", "path", chatDir, "error", addErr)
					continue
				}
				chatDirToRepo[chatDir] = ws.repo
				continue
			}
			// chatSessions does not exist yet — the workspace was just created
			// and the first chat may be minutes away. Watch the workspace dir
			// itself so the chatSessions creation is observed and promoted.
			if addErr := watcher.Add(ws.wsDir); addErr != nil {
				slog.Warn("Monitor: failed to watch new Copilot workspace dir", "path", ws.wsDir, "error", addErr)
				continue
			}
			pendingWorkspaces[ws.wsDir] = ws.repo

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			// Create covers new session files and directories; Write covers
			// appends to an existing session file (same rationale as
			// WatchRootForNewEntries).
			if !event.Has(fsnotify.Create) && !event.Has(fsnotify.Write) {
				continue
			}
			parent := filepath.Dir(event.Name)

			// Activity inside a watched chatSessions dir → the mapped repo.
			if repo, mapped := chatDirToRepo[parent]; mapped {
				onActivity(repo)
				continue
			}

			// chatSessions dir created inside a pending workspace: promote the
			// workspace-dir watch to a chatSessions watch. The creation itself
			// counts as activity — VS Code creates chatSessions when the first
			// chat message is sent — and firing now also covers a session file
			// written in the instant before the new watch lands.
			if repo, pending := pendingWorkspaces[parent]; pending {
				if filepath.Base(event.Name) != "chatSessions" {
					continue
				}
				if addErr := watcher.Add(event.Name); addErr != nil {
					slog.Warn("Monitor: failed to watch new Copilot chatSessions dir", "path", event.Name, "error", addErr)
					continue
				}
				chatDirToRepo[event.Name] = repo
				delete(pendingWorkspaces, parent)
				// The workspace-dir watch has served its purpose; drop it to
				// return the file descriptor.
				_ = watcher.Remove(parent)
				onActivity(repo)
				continue
			}

			// A new entry directly under workspaceStorage: possibly a
			// workspace directory VS Code just created for a newly opened
			// folder. Resolve it off-loop (workspace.json arrives async).
			if parent == root && !resolving[event.Name] {
				info, statErr := os.Stat(event.Name)
				if statErr != nil || !info.IsDir() {
					continue
				}
				resolving[event.Name] = true
				wsDir := event.Name
				go func() {
					repo, resolvedOK := resolveNewCopilotWorkspace(ctx, wsDir, resolver)
					select {
					case newWorkspaces <- resolvedWorkspace{wsDir: wsDir, repo: repo, ok: resolvedOK}:
					case <-ctx.Done():
					}
				}()
			}

		case watchErr, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			// Watcher errors are usually transient (overflow, races); keep
			// watching rather than tearing down the whole monitor.
			slog.Warn("Monitor: Copilot workspace watcher error", "root", root, "error", watchErr)
		}
	}
}

// mapCopilotWorkspaces enumerates every workspace directory under root ONCE
// and returns chatSessions-dir → repo for the workspaces that both map into
// the discovered-repo set and already have a chatSessions directory. This
// single ReadDir pass (instead of per-directory watches) is what keeps the
// monitor's file-descriptor usage independent of workspaceStorage size.
func mapCopilotWorkspaces(root string, resolver *Resolver) (map[string]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	chatDirToRepo := make(map[string]string)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		wsDir := filepath.Join(root, entry.Name())
		repo, ok, wsErr := copilotWorkspaceRepo(wsDir, resolver)
		if wsErr != nil || !ok {
			continue
		}
		chatDir := copilotide.GetChatSessionsPath(wsDir)
		if _, statErr := os.Stat(chatDir); statErr != nil {
			// No chat has ever happened in this workspace; skipped because
			// fsnotify cannot watch a missing directory (see the
			// WatchCopilotWorkspaceStorage doc comment).
			slog.Debug("Monitor: matched Copilot workspace has no chatSessions dir; skipping", "workspace", wsDir, "repo", repo)
			continue
		}
		chatDirToRepo[chatDir] = repo
	}
	return chatDirToRepo, nil
}

// copilotWorkspaceRepo resolves a workspace directory's workspace.json to the
// discovered repo its project folder lives in. Returns err when
// workspace.json is missing or unreadable — retryable, because VS Code writes
// it asynchronously after creating the directory — and ok=false with err=nil
// when the workspace resolves cleanly but maps to no discovered repo (an
// answer that will not change on retry).
//
// A multi-root (.code-workspace) workspace maps to the FIRST listed folder
// that falls inside a discovered repo: a chat event path alone cannot tell
// which folder of the window it belongs to, so one deterministic attribution
// is the best available.
func copilotWorkspaceRepo(wsDir string, resolver *Resolver) (string, bool, error) {
	ws, err := copilotide.ReadWorkspaceJSON(copilotide.GetWorkspaceMetadataPath(wsDir))
	if err != nil {
		return "", false, err
	}

	// Prefer workspace over folder, mirroring the copilotide provider's own
	// workspace matching.
	uri := ws.Workspace
	if uri == "" {
		uri = ws.Folder
	}
	if uri == "" {
		// Valid JSON with neither field (e.g. an untitled workspace): not
		// retryable, just unmappable.
		return "", false, nil
	}

	p, err := copilotide.URIToPath(uri)
	if err != nil {
		// Non-file URIs (remote workspaces etc.) can never map to a local repo.
		return "", false, nil
	}

	if strings.HasSuffix(p, ".code-workspace") {
		for _, folder := range copilotide.CollectCodeWorkspaceFolders(p) {
			if repo, ok := resolver.repoContaining(folder); ok {
				return repo, true, nil
			}
		}
		return "", false, nil
	}

	repo, ok := resolver.repoContaining(p)
	return repo, ok, nil
}

// resolveNewCopilotWorkspace maps a freshly created workspace directory to a
// discovered repo, retrying while workspace.json is missing or unparsable
// (VS Code writes it a moment after creating the directory). It stops early —
// without burning the remaining retries — once a valid workspace.json maps to
// a path outside every discovered repo.
func resolveNewCopilotWorkspace(ctx context.Context, wsDir string, resolver *Resolver) (string, bool) {
	for attempt := 0; attempt < copilotWorkspaceJSONRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", false
			case <-time.After(copilotWorkspaceJSONRetryDelay):
			}
		}
		repo, ok, err := copilotWorkspaceRepo(wsDir, resolver)
		if err != nil {
			continue // workspace.json not written yet; retry.
		}
		return repo, ok
	}
	slog.Debug("Monitor: gave up waiting for workspace.json in new Copilot workspace dir", "path", wsDir)
	return "", false
}
