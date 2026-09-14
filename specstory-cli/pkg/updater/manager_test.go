package updater

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func write(t *testing.T, name string, data []byte) {
	t.Helper()
	must(t, os.WriteFile(name, data, 0o755))
}
func contents(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	must(t, err)
	return data
}
func archiveFor(t *testing.T, goos string, payload []byte, names ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	if goos == "windows" {
		w := zip.NewWriter(&b)
		for _, name := range names {
			entry, err := w.Create(name)
			must(t, err)
			_, err = entry.Write(payload)
			must(t, err)
		}
		must(t, w.Close())
	} else {
		gz := gzip.NewWriter(&b)
		w := tar.NewWriter(gz)
		for _, name := range names {
			must(t, w.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg}))
			_, err := w.Write(payload)
			must(t, err)
		}
		must(t, w.Close())
		must(t, gz.Close())
	}
	return b.Bytes()
}

type releaseFixture struct {
	version      string
	archive      []byte
	manifest     string
	latestStatus int
	redirect     string
	requests     []string
}

func fixture(t *testing.T, goos, arch string, old, next []byte) (*Manager, *releaseFixture) {
	t.Helper()
	root := t.TempDir()
	executable := filepath.Join(root, "specstory")
	binaryName := "specstory"
	if goos == "windows" {
		executable += ".exe"
		binaryName += ".exe"
	}
	write(t, executable, old)
	f := &releaseFixture{version: "2.0.0", archive: archiveFor(t, goos, next, binaryName)}
	name, err := assetName(goos, arch)
	must(t, err)
	f.manifest = digest(f.archive) + "  " + name + "\n"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/releases/latest":
			if f.latestStatus != 0 {
				w.WriteHeader(f.latestStatus)
				return
			}
			location := "/releases/tag/v" + f.version
			if f.redirect != "" {
				location = f.redirect
			}
			w.Header().Set("Location", location)
			w.WriteHeader(http.StatusFound)
		case "/releases/download/v" + f.version + "/" + name:
			_, _ = w.Write(f.archive)
		case "/releases/download/v" + f.version + "/SpecStoryCLI_" + f.version + "_checksums.txt":
			_, _ = io.WriteString(w, f.manifest)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	m := &Manager{Version: "1.0.0", Executable: executable, Kind: "native", statePath: filepath.Join(root, "state", "update.json"), goos: goos, arch: arch, releases: server.URL + "/releases", client: server.Client()}
	m.verify = func(_ context.Context, data []byte, version string) error {
		if string(data) != version {
			return errors.New("incorrect binary version")
		}
		return nil
	}
	return m, f
}

func TestUpdateAndRollbackAllTargets(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			t.Run(goos+"/"+arch, func(t *testing.T) {
				m, f := fixture(t, goos, arch, []byte("1.0.0"), []byte("2.0.0"))
				status, err := m.Run(t.Context(), false, false)
				must(t, err)
				if status.Installed != "2.0.0" || string(contents(t, m.Executable)) != "2.0.0" || string(contents(t, m.Executable+".previous")) != "1.0.0" {
					t.Fatalf("incorrect replacement: %+v", status)
				}
				if len(f.requests) != 3 {
					t.Fatalf("requests: %v", f.requests)
				}
				m.Version = "2.0.0"
				status, err = m.Rollback(t.Context())
				must(t, err)
				if !status.Paused || status.Installed != "1.0.0" || string(contents(t, m.Executable)) != "1.0.0" {
					t.Fatalf("incorrect rollback: %+v", status)
				}
				m.Version = "1.0.0"
				_, err = m.Run(t.Context(), false, true)
				must(t, err)
				if len(f.requests) != 3 {
					t.Fatal("rollback did not pause background updates")
				}
				_, err = m.Run(t.Context(), false, false)
				must(t, err)
				if m.Status().Paused {
					t.Fatal("explicit update did not resume updates")
				}
			})
		}
	}
}

func TestRejectedUpdatesPreserveInstallation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Manager, *releaseFixture)
	}{
		{"checksum mismatch", func(m *Manager, f *releaseFixture) {
			f.manifest = strings.Repeat("0", 64) + "  SpecStoryCLI_Linux_x86_64.tar.gz"
		}},
		{"missing checksum", func(m *Manager, f *releaseFixture) { f.manifest = "" }},
		{"duplicate checksum", func(m *Manager, f *releaseFixture) { f.manifest += f.manifest }},
		{"wrong binary version", func(m *Manager, f *releaseFixture) {
			m.verify = func(_ context.Context, b []byte, _ string) error {
				if string(b) == "2.0.0" {
					return errors.New("bad version")
				}
				return nil
			}
		}},
		{"bad latest status", func(m *Manager, f *releaseFixture) { f.latestStatus = 503 }},
		{"untrusted latest redirect", func(m *Manager, f *releaseFixture) { f.redirect = "https://example.com/releases/tag/v2.0.0" }},
		{"prerelease", func(m *Manager, f *releaseFixture) { f.version = "2.0.0-rc.1" }},
		{"invalid tag", func(m *Manager, f *releaseFixture) { f.version = "2.0.0/extra" }},
		{"Homebrew", func(m *Manager, f *releaseFixture) { m.Kind = "homebrew" }},
		{"package managed", func(m *Manager, f *releaseFixture) { m.Kind = "managed" }},
		{"unsupported platform", func(m *Manager, f *releaseFixture) { m.arch = "386" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, f := fixture(t, "linux", "amd64", []byte("1.0.0"), []byte("2.0.0"))
			c.change(m, f)
			_, err := m.Run(t.Context(), false, false)
			if err == nil {
				t.Fatal("expected rejection")
			}
			if string(contents(t, m.Executable)) != "1.0.0" {
				t.Fatal("old executable changed")
			}
			if _, err := os.Stat(m.Executable + ".previous"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("created backup before validation")
			}
		})
	}
}

func TestReadOnlyCheckDoesNotLockOrThrottle(t *testing.T) {
	m, f := fixture(t, "linux", "amd64", []byte("1.0.0"), []byte("2.0.0"))
	lock, err := lockInstallation(m.Executable)
	must(t, err)
	defer func() { _ = lock.Close() }()
	m.Kind = "homebrew"
	s, err := m.Run(t.Context(), true, false)
	must(t, err)
	if s.Latest != "2.0.0" || len(f.requests) != 1 || !m.Status().CheckedAt.IsZero() {
		t.Fatalf("check mutated or downloaded: %+v", s)
	}
}
func TestNoDowngradeAndExplicitResume(t *testing.T) {
	for _, version := range []string{"2.0.0", "1.0.0"} {
		t.Run(version, func(t *testing.T) {
			m, f := fixture(t, "linux", "amd64", []byte("2.0.0"), []byte("2.0.0"))
			m.Version = "2.0.0"
			f.version = version
			must(t, m.save(Status{Paused: true, Installed: "0.9.0"}))
			status, err := m.Run(t.Context(), false, false)
			must(t, err)
			if status.Paused || status.Installed != "2.0.0" || len(f.requests) != 1 {
				t.Fatalf("bad unchanged status: %+v", status)
			}
		})
	}
}
func TestLocksAndExternalReplacement(t *testing.T) {
	m, _ := fixture(t, "linux", "amd64", []byte("1.0.0"), []byte("2.0.0"))
	lock, err := lockInstallation(m.Executable)
	must(t, err)
	_, err = m.Run(t.Context(), false, false)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("missing concurrency lock: %v", err)
	}
	must(t, lock.Close())
	m.verify = func(_ context.Context, b []byte, _ string) error {
		if string(b) == "2.0.0" {
			write(t, m.Executable, []byte("external update"))
		}
		return nil
	}
	_, err = m.Run(t.Context(), false, false)
	if err == nil || !strings.Contains(err.Error(), "changed during") {
		t.Fatalf("did not detect external update: %v", err)
	}
	if string(contents(t, m.Executable)) != "external update" {
		t.Fatal("overwrote external update")
	}
}
func TestRollbackRejectsTamperedBackup(t *testing.T) {
	m, _ := fixture(t, "linux", "amd64", []byte("1.0.0"), []byte("2.0.0"))
	_, err := m.Run(t.Context(), false, false)
	must(t, err)
	m.Version = "2.0.0"
	write(t, m.Executable+".previous", []byte("tampered"))
	_, err = m.Rollback(t.Context())
	if err == nil {
		t.Fatal("accepted tampered backup")
	}
	if string(contents(t, m.Executable)) != "2.0.0" {
		t.Fatal("rollback damaged current installation")
	}
}
func TestAutomaticPolicyAndCache(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("SPECSTORY_NO_AUTO_UPDATE", "")
	m, _ := fixture(t, "linux", "amd64", []byte("1.0.0"), []byte("2.0.0"))
	now := time.Now()
	if !m.Due(now) {
		t.Fatal("new native installation should check")
	}
	for _, s := range []Status{{CheckedAt: now}, {Paused: true}} {
		must(t, m.save(s))
		if m.Due(now) {
			t.Fatal("ignored cache/pause")
		}
	}
	must(t, m.save(Status{CheckedAt: now.Add(-7 * time.Hour)}))
	if !m.Due(now) {
		t.Fatal("expired check was skipped")
	}
	for _, kind := range []string{"homebrew", "manual", "managed"} {
		m.Kind = kind
		if m.Due(now) {
			t.Fatal("updated managed/custom installation")
		}
	}
	m.Kind = "native"
	m.Version = "dev"
	if m.Due(now) {
		t.Fatal("updated development build")
	}
	m.Version = "1.0.0"
	t.Setenv("CI", "true")
	if m.Due(now) {
		t.Fatal("updated in CI")
	}
	t.Setenv("CI", "")
	t.Setenv("SPECSTORY_NO_AUTO_UPDATE", "1")
	if m.Due(now) {
		t.Fatal("ignored opt-out")
	}
}
func TestInstallOwnership(t *testing.T) {
	root := t.TempDir()
	native := filepath.Join(root, ".local", "bin", "specstory")
	must(t, os.MkdirAll(filepath.Dir(native), 0o755))
	write(t, native, []byte("native"))
	native, err := filepath.EvalSymlinks(native)
	must(t, err)
	if installationKind(native, root, "", "linux") != "native" {
		t.Fatal("default installer not recognized")
	}
	if installationKind("/opt/homebrew/Cellar/specstory/1.0.0/bin/specstory", root, "", "darwin") != "homebrew" {
		t.Fatal("Homebrew not recognized")
	}
	if installationKind("/usr/bin/specstory", root, "", "linux") != "managed" {
		t.Fatal("system package not recognized")
	}
	if installationKind("/usr/local/bin/specstory", root, "", "darwin") != "manual" {
		t.Fatal("legacy installer or arbitrary /usr/local/bin binary became auto-managed")
	}
	if runtime.GOOS != "windows" {
		target := filepath.Join(root, "manual")
		write(t, target, []byte("manual"))
		must(t, os.Remove(native))
		must(t, os.Symlink(target, native))
		if installationKind(target, root, "", "linux") != "manual" {
			t.Fatal("symlink transferred ownership")
		}
	}
}

// This is a native integration test: an old executable remains running while
// the library replaces its path, and the next launch executes the new version.
func TestUpdateRunningBinary(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "fixture.go")
	write(t, source, []byte(`package main
import("fmt";"os";"io")
var version string
func main(){if len(os.Args)>1&&os.Args[1]=="hold"{fmt.Println("ready");_,_=io.Copy(io.Discard,os.Stdin);return};fmt.Println(version+" (SpecStory)")}
`))
	build := func(version string) []byte {
		name := filepath.Join(root, "fixture-"+version)
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		cmd := exec.CommandContext(t.Context(), "go", "build", "-ldflags", "-X main.version="+version, "-o", name, source)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("build fixture: %v\n%s", err, output)
		}
		return contents(t, name)
	}
	old, next := build("1.0.0"), build("2.0.0")
	m, _ := fixture(t, runtime.GOOS, runtime.GOARCH, old, next)
	m.verify = m.verifyBinary
	process := exec.CommandContext(t.Context(), m.Executable, "hold")
	stdin, err := process.StdinPipe()
	must(t, err)
	stdout, err := process.StdoutPipe()
	must(t, err)
	must(t, process.Start())
	defer func() { _ = stdin.Close(); _ = process.Wait() }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	must(t, err)
	if strings.TrimSpace(line) != "ready" {
		t.Fatal("old process not ready")
	}
	_, err = m.Run(t.Context(), false, false)
	must(t, err)
	output, err := exec.CommandContext(t.Context(), m.Executable, "--version").CombinedOutput()
	must(t, err)
	if strings.TrimSpace(string(output)) != "2.0.0 (SpecStory)" {
		t.Fatalf("next launch: %s", output)
	}
	// Finish the running old process before rolling back (Windows retains its lock).
	must(t, stdin.Close())
	must(t, process.Wait())
	m.Version = "2.0.0"
	_, err = m.Rollback(t.Context())
	must(t, err)
	output, err = exec.CommandContext(t.Context(), m.Executable, "--version").CombinedOutput()
	must(t, err)
	if strings.TrimSpace(string(output)) != "1.0.0 (SpecStory)" {
		t.Fatal("native rollback failed")
	}
}

func TestInvalidArchivesAndLimits(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			name := "specstory"
			if goos == "windows" {
				name += ".exe"
			}
			for _, names := range [][]string{{"../" + name}, {name, name}, {"readme"}} {
				if _, err := extractBinary(archiveFor(t, goos, []byte("binary"), names...), goos); err == nil {
					t.Fatalf("accepted unsafe/missing archive: %v", names)
				}
			}
			if _, err := extractBinary([]byte("not an archive"), goos); err == nil {
				t.Fatal("accepted corrupt archive")
			}
		})
	}
	if _, err := readLimited(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("read limit not enforced")
	}
	if _, err := stableVersion("v2.0.0+metadata"); err == nil {
		t.Fatal("accepted unstable build")
	}
	for _, address := range []string{"http://github.com/x", "https://github.com.evil.test/x", "https://user@github.com/x", "https://github.com:444/x"} {
		req, err := http.NewRequest(http.MethodGet, address, http.NoBody)
		must(t, err)
		if officialRedirect(req, nil) == nil {
			t.Fatalf("accepted redirect %s", address)
		}
	}
}

func TestCanceledDownloadPreservesBinary(t *testing.T) {
	m, _ := fixture(t, "linux", "amd64", []byte("1.0.0"), []byte("2.0.0"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := m.Run(ctx, false, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not propagated: %v", err)
	}
	if string(contents(t, m.Executable)) != "1.0.0" {
		t.Fatal("canceled update changed binary")
	}
}

func TestProbeRejectsInvalidExecutable(t *testing.T) {
	m, _ := fixture(t, runtime.GOOS, runtime.GOARCH, []byte("1.0.0"), []byte("2.0.0"))
	if err := m.verifyBinary(t.Context(), []byte("not an executable"), "2.0.0"); err == nil {
		t.Fatal("invalid executable passed probe")
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(m.Executable), ".specstory-verify-*"))
	must(t, err)
	if len(matches) != 0 {
		t.Fatalf("leaked staged probes: %v", matches)
	}
}

func TestProbeOutputLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	output := &probeOutput{cancel: cancel}
	payload := bytes.Repeat([]byte("x"), maxProbeOutput)
	_, err := output.Write(payload)
	must(t, err)
	if output.exceeded || ctx.Err() != nil {
		t.Fatal("rejected output exactly at the cap")
	}
	for range 3 {
		_, err = output.Write(payload)
		must(t, err)
	}
	if !output.exceeded || !errors.Is(ctx.Err(), context.Canceled) || !bytes.Equal(output.buffer.Bytes(), payload) {
		t.Fatalf("unbounded probe: exceeded=%v canceled=%v bytes=%d", output.exceeded, ctx.Err(), output.buffer.Len())
	}
}

func TestProbeRejectsExcessOutput(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "noisy.go")
	write(t, source, []byte(`package main
import("fmt";"os";"strings";"time")
func main(){fmt.Fprint(os.Stdout,strings.Repeat("o",3072));fmt.Fprint(os.Stderr,strings.Repeat("e",3072));time.Sleep(30*time.Second)}
`))
	name := filepath.Join(root, "noisy")
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", name, source)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build noisy probe: %v\n%s", err, output)
	}
	m, _ := fixture(t, runtime.GOOS, runtime.GOARCH, []byte("1.0.0"), []byte("2.0.0"))
	started := time.Now()
	err := m.verifyBinary(t.Context(), contents(t, name), "2.0.0")
	if err == nil || !strings.Contains(err.Error(), "output limit") {
		t.Fatalf("noisy binary was not rejected at the shared output cap: %v", err)
	}
	if time.Since(started) >= 8*time.Second {
		t.Fatal("noisy probe waited for its timeout instead of being canceled")
	}
	if string(contents(t, m.Executable)) != "1.0.0" {
		t.Fatal("noisy probe changed the installed executable")
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(m.Executable), ".specstory-verify-*"))
	must(t, err)
	if len(matches) != 0 {
		t.Fatalf("leaked staged probes: %v", matches)
	}
}

func TestReplacementRefusesLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires additional Windows privileges")
	}
	for _, suffix := range []string{"target", "backup", "staged", "lock"} {
		t.Run(suffix, func(t *testing.T) {
			m, _ := fixture(t, "linux", "amd64", []byte("1.0.0"), []byte("2.0.0"))
			outside := filepath.Join(t.TempDir(), "outside")
			write(t, outside, []byte("unrelated"))
			link := m.Executable
			switch suffix {
			case "target":
				must(t, os.Remove(link))
			case "backup":
				link += ".previous"
			case "staged":
				link = filepath.Join(filepath.Dir(link), ".specstory.new")
			case "lock":
				link = filepath.Join(filepath.Dir(link), ".specstory.update.lock")
			}
			must(t, os.Symlink(outside, link))
			_, err := m.Run(t.Context(), false, false)
			if err == nil {
				t.Fatal("followed an update symlink")
			}
			if string(contents(t, outside)) != "unrelated" {
				t.Fatal("modified symlink target")
			}
		})
	}
}

func TestWorkerDetaches(t *testing.T) {
	if binary := os.Getenv("SPECSTORY_TEST_DETACH_BINARY"); binary != "" {
		m := &Manager{Executable: binary}
		if err := m.spawn(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	root := t.TempDir()
	source := filepath.Join(root, "worker.go")
	binary := filepath.Join(root, "worker")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	write(t, source, []byte(`package main
import("os";"time")
func main(){if len(os.Args)!=2||os.Args[1]!="__specstory_update"{os.Exit(2)};time.Sleep(250*time.Millisecond);if err:=os.WriteFile("detached.txt",[]byte("finished after parent exited"),0600);err!=nil{os.Exit(3)}}
`))
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", binary, source)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build worker: %v\n%s", err, output)
	}
	child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestWorkerDetaches$")
	child.Env = append(os.Environ(), "SPECSTORY_TEST_DETACH_BINARY="+binary)
	output, err = child.CombinedOutput()
	if err != nil {
		t.Fatalf("launcher: %v\n%s", err, output)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(filepath.Join(root, "detached.txt")); err == nil && string(data) == "finished after parent exited" {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("detached worker did not survive parent exit")
}

func TestRollbackRefusesExternalVersionChange(t *testing.T) {
	m, _ := fixture(t, "linux", "amd64", []byte("1.0.0"), []byte("2.0.0"))
	_, err := m.Run(t.Context(), false, false)
	must(t, err)
	m.Version = "2.0.0"
	write(t, m.Executable, []byte("3.0.0"))
	_, err = m.Rollback(t.Context())
	if err == nil {
		t.Fatal("rollback overwrote a different installed version")
	}
	if string(contents(t, m.Executable)) != "3.0.0" {
		t.Fatal("external installation was modified")
	}
}
