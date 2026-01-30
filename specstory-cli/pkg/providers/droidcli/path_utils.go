package droidcli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

const (
	factoryRootDir     = ".factory"
	factorySessionsDir = "sessions"
)

type sessionFile struct {
	Path     string
	ModTime  int64
	FileName string
}

func resolveSessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("droidcli: cannot resolve home dir: %w", err)
	}
	dir := filepath.Join(home, factoryRootDir, factorySessionsDir)
	return dir, nil
}

func listSessionFiles() ([]sessionFile, error) {
	dir, err := resolveSessionsDir()
	if err != nil {
		return nil, err
	}
	files := make([]sessionFile, 0, 64)
	walker := func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(d.Name()) != ".jsonl" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, sessionFile{
			Path:     path,
			ModTime:  info.ModTime().UnixNano(),
			FileName: d.Name(),
		})
		return nil
	}
	if err := filepath.WalkDir(dir, walker); err != nil {
		if os.IsNotExist(err) {
			return []sessionFile{}, nil
		}
		return nil, fmt.Errorf("droidcli: unable to read sessions dir: %w", err)
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime > files[j].ModTime
	})
	return files, nil
}

func findSessionFileByID(sessionID string) (string, error) {
	files, err := listSessionFiles()
	if err != nil {
		return "", err
	}
	for _, file := range files {
		candidate := file.FileName
		if candidate == sessionID || trimExt(candidate) == sessionID {
			return file.Path, nil
		}
	}
	return "", nil
}

func trimExt(name string) string {
	ext := filepath.Ext(name)
	if ext == "" {
		return name
	}
	return name[:len(name)-len(ext)]
}
