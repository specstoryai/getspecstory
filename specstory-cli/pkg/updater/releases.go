// Package updater installs verified CLI releases without restarting active sessions.
package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Masterminds/semver/v3"
)

const releaseURL = "https://github.com/specstoryai/getspecstory/releases"
const maxDownload = 128 << 20

func stableVersion(value string) (*semver.Version, error) {
	v, err := semver.StrictNewVersion(strings.TrimPrefix(value, "v"))
	if err != nil || v.Prerelease() != "" || v.Metadata() != "" {
		return nil, fmt.Errorf("expected a stable release version, got %q", value)
	}
	return v, nil
}

// assetName deliberately matches GoReleaser and the website installers exactly.
func assetName(goos, arch string) (string, error) {
	platform := map[string]string{"darwin": "Darwin", "linux": "Linux", "windows": "Windows"}[goos]
	architecture := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[arch]
	if platform == "" || architecture == "" {
		return "", fmt.Errorf("automatic updates are unavailable for %s/%s", goos, arch)
	}
	extension := ".tar.gz"
	if goos == "windows" {
		extension = ".zip"
	}
	return "SpecStoryCLI_" + platform + "_" + architecture + extension, nil
}

func (m *Manager) latest(ctx context.Context) (string, error) {
	// The public redirect avoids GitHub API credentials and its lower API quota.
	client := *m.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, m.releases+"/latest", http.NoBody)
	if err != nil {
		return "", err
	}
	response, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 300 || response.StatusCode > 399 {
		return "", fmt.Errorf("release lookup returned HTTP %d", response.StatusCode)
	}
	location, err := response.Location()
	if err != nil {
		return "", err
	}
	prefix := m.releases + "/tag/v"
	if !strings.HasPrefix(location.String(), prefix) {
		return "", errors.New("release lookup did not return an official SpecStory tag")
	}
	version, err := stableVersion(strings.TrimPrefix(location.String(), prefix))
	if err != nil {
		return "", err
	}
	return version.String(), nil
}

func (m *Manager) download(ctx context.Context, address string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, http.NoBody)
	if err != nil {
		return nil, err
	}
	response, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned HTTP %d", response.StatusCode)
	}
	return readLimited(response.Body, limit)
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if int64(len(data)) > limit {
		return nil, errors.New("release file exceeds the size limit")
	}
	return data, err
}

func verifyChecksum(name string, data, manifest []byte) error {
	var expected []byte
	count := 0
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[1] != name {
			continue
		}
		count++
		var err error
		expected, err = hex.DecodeString(fields[0])
		if err != nil || len(expected) != sha256.Size {
			return errors.New("invalid release checksum")
		}
	}
	if count != 1 {
		return errors.New("release must contain exactly one checksum for the selected archive")
	}
	actual := sha256.Sum256(data)
	if !bytes.Equal(actual[:], expected) {
		return errors.New("release checksum mismatch; existing installation is unchanged")
	}
	return nil
}

// extractBinary reads only the root executable, never paths or links in the archive.
func extractBinary(archive []byte, goos string) ([]byte, error) {
	var binary []byte
	if goos == "windows" {
		reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, file := range reader.File {
			if file.Name != "specstory.exe" {
				continue
			}
			if binary != nil || !file.Mode().IsRegular() {
				return nil, errors.New("archive must contain one regular SpecStory executable")
			}
			entry, err := file.Open()
			if err != nil {
				return nil, err
			}
			binary, err = readLimited(entry, maxDownload)
			_ = entry.Close()
			if err != nil {
				return nil, err
			}
		}
	} else {
		compressed, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			return nil, err
		}
		defer func() { _ = compressed.Close() }()
		// Bound the entire expanded archive, including entries we skip.
		reader := tar.NewReader(io.LimitReader(compressed, maxDownload+1))
		for {
			header, err := reader.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			if header.Name != "specstory" {
				continue
			}
			if binary != nil || header.Typeflag != tar.TypeReg {
				return nil, errors.New("archive must contain one regular SpecStory executable")
			}
			binary, err = readLimited(reader, maxDownload)
			if err != nil {
				return nil, err
			}
		}
	}
	if len(binary) == 0 {
		return nil, errors.New("release archive is missing the SpecStory executable")
	}
	return binary, nil
}

func officialRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("too many release redirects")
	}
	if !officialURL(req.URL) {
		return errors.New("release redirected outside GitHub's HTTPS download hosts")
	}
	return nil
}

func officialURL(address *url.URL) bool {
	host := strings.ToLower(address.Hostname())
	return address.Scheme == "https" && address.User == nil && (address.Port() == "" || address.Port() == "443") &&
		(host == "github.com" || strings.HasSuffix(host, ".githubusercontent.com"))
}
