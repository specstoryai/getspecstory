package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	apply "github.com/creativeprojects/go-selfupdate/update"
)

const checkInterval = 6 * time.Hour
const workerTimeout = 3 * time.Minute

// ErrBusy means another process owns this installation's update lock.
var ErrBusy = errors.New("another SpecStory update is running")

// Status is per installation, not per project. Rollback pauses automatic updates.
type Status struct {
	CheckedAt      time.Time `json:"checked_at"`
	Latest         string    `json:"latest,omitempty"`
	Installed      string    `json:"installed,omitempty"`
	Previous       string    `json:"previous,omitempty"`
	PreviousSHA256 string    `json:"previous_sha256,omitempty"`
	Error          string    `json:"error,omitempty"`
	Paused         bool      `json:"paused,omitempty"`
}

// Manager keeps policy separate from the verified download and replacement steps.
type Manager struct {
	Version    string
	Executable string
	Kind       string
	statePath  string
	goos       string
	arch       string
	releases   string
	client     *http.Client
	verify     func(context.Context, []byte, string) error
}

// New resolves the executable actually running, so a shadowed PATH entry or a
// Homebrew symlink cannot redirect an update into an unrelated installation.
func New(version string) (*Manager, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(executable))
	m := &Manager{
		Version: version, Executable: executable,
		Kind:      installationKind(executable, home, os.Getenv("LOCALAPPDATA"), runtime.GOOS),
		statePath: filepath.Join(home, ".specstory", "cli", "updates", hex.EncodeToString(key[:8])+".json"),
		goos:      runtime.GOOS, arch: runtime.GOARCH, releases: releaseURL,
		client: &http.Client{Timeout: workerTimeout, CheckRedirect: officialRedirect},
	}
	m.verify = m.verifyBinary
	return m, nil
}

func installationKind(executable, home, localAppData, goos string) string {
	normalized := strings.ToLower(filepath.ToSlash(executable))
	if strings.Contains(normalized, "/cellar/specstory/") {
		return "homebrew"
	}
	if strings.Contains(normalized, "/nix/store/") || strings.HasPrefix(normalized, "/usr/bin/") || strings.HasPrefix(normalized, "/bin/") {
		return "managed"
	}
	native := filepath.Join(home, ".local", "bin", "specstory")
	if goos == "windows" {
		if localAppData == "" {
			return "manual"
		}
		native = filepath.Join(localAppData, "SpecStory", "bin", "specstory.exe")
	}
	// A link at the default path does not transfer ownership of its target.
	if info, err := os.Lstat(native); err == nil && !info.Mode().IsRegular() {
		return "manual"
	}
	if resolved, err := filepath.EvalSymlinks(native); err == nil {
		native = resolved
	}
	if filepath.Clean(executable) == filepath.Clean(native) || goos == "windows" && strings.EqualFold(executable, native) {
		return "native"
	}
	return "manual"
}

// Status reads cached diagnostics without accessing the network.
func (m *Manager) Status() Status {
	var status Status
	data, err := os.ReadFile(m.statePath)
	if err == nil {
		_ = json.Unmarshal(data, &status)
	}
	return status
}

func (m *Manager) save(status Status) error {
	if err := os.MkdirAll(filepath.Dir(m.statePath), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(m.statePath), ".update-state-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if err = errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), m.statePath)
}

// Due excludes development builds, package managers, CI, and explicit opt-outs.
func (m *Manager) Due(now time.Time) bool {
	if m.Kind != "native" || os.Getenv("SPECSTORY_NO_AUTO_UPDATE") == "1" || os.Getenv("CI") != "" {
		return false
	}
	if _, err := stableVersion(m.Version); err != nil {
		return false
	}
	status := m.Status()
	return !status.Paused && (status.CheckedAt.IsZero() || now.Sub(status.CheckedAt) >= checkInterval || status.CheckedAt.After(now))
}

// Run checks or installs an update. Automatic callers recheck the cache inside
// an OS lock; concurrent terminals can never replace the binary simultaneously.
func (m *Manager) Run(ctx context.Context, checkOnly, automatic bool) (status Status, err error) {
	if _, err = stableVersion(m.Version); err != nil {
		return status, err
	}
	if checkOnly {
		status = m.Status()
		status.Latest, err = m.latest(ctx)
		return status, err
	}
	if !checkOnly && m.Kind == "homebrew" {
		return status, errors.New("this installation is managed by Homebrew; run: brew upgrade specstoryai/tap/specstory")
	}
	if !checkOnly && m.Kind == "managed" {
		return status, errors.New("a package manager owns this installation; update it with that package manager")
	}
	if _, err = assetName(m.goos, m.arch); err != nil {
		return status, err
	}
	lock, err := lockInstallation(m.Executable)
	if err != nil {
		return status, err
	}
	defer func() { _ = lock.Close() }()
	status = m.Status()
	if automatic && !m.Due(time.Now()) {
		return status, nil
	}
	status.Installed = m.Version
	defer func() {
		status.CheckedAt = time.Now().UTC()
		status.Error = ""
		if err != nil {
			status.Error = err.Error()
		}
		if saveErr := m.save(status); saveErr != nil {
			err = errors.Join(err, fmt.Errorf("could not save update status: %w", saveErr))
		}
	}()
	status.Latest, err = m.latest(ctx)
	if err != nil {
		return status, err
	}
	current, _ := stableVersion(m.Version)
	latest, _ := stableVersion(status.Latest)
	if !latest.GreaterThan(current) {
		if !automatic {
			status.Paused = false
		}
		return status, nil
	}
	old, err := readRegular(m.Executable)
	if err != nil {
		return status, err
	}
	if err = m.verify(ctx, old, m.Version); err != nil {
		return status, fmt.Errorf("installed binary no longer matches this process; restart SpecStory and retry: %w", err)
	}
	name, _ := assetName(m.goos, m.arch)
	base := m.releases + "/download/v" + status.Latest + "/"
	manifest, err := m.download(ctx, base+"SpecStoryCLI_"+status.Latest+"_checksums.txt", 1<<20)
	if err != nil {
		return status, err
	}
	archive, err := m.download(ctx, base+name, maxDownload)
	if err != nil {
		return status, err
	}
	if err = verifyChecksum(name, archive, manifest); err != nil {
		return status, err
	}
	binary, err := extractBinary(archive, m.goos)
	if err != nil {
		return status, err
	}
	if err = m.verify(ctx, binary, status.Latest); err != nil {
		return status, err
	}
	if err = m.replace(binary, old); err != nil {
		return status, err
	}
	status.Installed, status.Previous, status.PreviousSHA256 = status.Latest, m.Version, digest(old)
	status.Paused = false
	return status, nil
}

// Rollback swaps back to the saved, hash-verified previous binary and pauses
// background updates until a successful explicit `specstory update`.
func (m *Manager) Rollback(ctx context.Context) (Status, error) {
	if m.Kind == "homebrew" || m.Kind == "managed" {
		return Status{}, errors.New("use your package manager to roll back this installation")
	}
	lock, err := lockInstallation(m.Executable)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = lock.Close() }()
	status := m.Status()
	previous, err := readRegular(m.Executable + ".previous")
	if err != nil || status.Previous == "" || digest(previous) != status.PreviousSHA256 {
		return status, errors.New("no verified previous version is available; reinstall the desired release")
	}
	if err = m.verify(ctx, previous, status.Previous); err != nil {
		return status, err
	}
	current, err := readRegular(m.Executable)
	if err != nil {
		return status, err
	}
	if err = m.replace(previous, current); err != nil {
		return status, err
	}
	status.Installed, status.Previous, status.PreviousSHA256 = status.Previous, m.Version, digest(current)
	status.Paused, status.Error = true, ""
	return status, m.save(status)
}

func readRegular(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("refusing to replace a symlink or non-regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return readLimited(file, maxDownload)
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (m *Manager) replace(binary, original []byte) error {
	current, err := readRegular(m.Executable)
	if err != nil || !bytes.Equal(current, original) {
		return errors.New("installation changed during download; retry the update")
	}
	for _, path := range []string{m.Executable + ".previous", filepath.Join(filepath.Dir(m.Executable), "."+filepath.Base(m.Executable)+".new")} {
		info, err := os.Lstat(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("refusing a non-regular update file: %s", path)
		}
	}
	staged := filepath.Join(filepath.Dir(m.Executable), "."+filepath.Base(m.Executable)+".new")
	defer func() { _ = os.Remove(staged) }()
	err = apply.Apply(bytes.NewReader(binary), apply.Options{TargetPath: m.Executable, OldSavePath: m.Executable + ".previous"})
	if rollbackErr := apply.RollbackError(err); rollbackErr != nil {
		return fmt.Errorf("update and restoration failed (%v; %v); recover %s.previous or rerun the installer", err, rollbackErr, m.Executable)
	}
	if err != nil {
		return fmt.Errorf("could not replace SpecStory; close other SpecStory processes and retry: %w", err)
	}
	return nil
}

func (m *Manager) verifyBinary(ctx context.Context, binary []byte, version string) error {
	// Stage on the executable's volume: download/temp directories may be noexec.
	pattern := ".specstory-verify-*"
	if m.goos == "windows" {
		pattern += ".exe"
	}
	file, err := os.CreateTemp(filepath.Dir(m.Executable), pattern)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(binary)
	closeErr := file.Close()
	if err = errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	if err = os.Chmod(file.Name(), 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, file.Name(), "--no-usage-analytics", "--no-version-check", "--version")
	output, err := command.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != version+" (SpecStory)" {
		return errors.New("downloaded binary failed its version check; existing installation is unchanged")
	}
	return nil
}
