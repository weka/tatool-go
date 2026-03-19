package script

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultRepo   = "weka/tools"
	defaultBranch = "master"
	scriptSubpath = "install/scripts.d/ta/"
)

// SyncFromGitHub fetches the latest ta scripts from the weka/tools repo
// and writes them to ~/.tatool/scripts/.
func SyncFromGitHub(repo, branch string) (int, error) {
	if repo == "" {
		repo = defaultRepo
	}
	if branch == "" {
		branch = defaultBranch
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/tarball/%s", repo, branch)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	// Use GITHUB_TOKEN if available for private repos / rate limits
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetching tarball: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	destDir := cachedScriptsDir()
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return 0, fmt.Errorf("creating cache dir: %w", err)
	}

	// Clear existing cached scripts
	entries, _ := os.ReadDir(destDir)
	for _, e := range entries {
		os.Remove(filepath.Join(destDir, e.Name()))
	}

	count, err := extractTAScripts(resp.Body, destDir)
	if err != nil {
		return 0, fmt.Errorf("extracting scripts: %w", err)
	}

	// Write timestamp
	ts := filepath.Join(filepath.Dir(destDir), ".last-updated")
	os.WriteFile(ts, []byte(time.Now().UTC().Format(time.RFC3339)), 0644)

	return count, nil
}

// extractTAScripts reads a gzipped tarball and extracts only files under
// the install/scripts.d/ta/ subtree to destDir.
func extractTAScripts(r io.Reader, destDir string) (int, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	count := 0

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, fmt.Errorf("reading tar: %w", err)
		}

		// GitHub tarballs have a top-level dir like "weka-tools-abc1234/"
		// We need to find the scriptSubpath after that prefix.
		parts := strings.SplitN(hdr.Name, "/", 2)
		if len(parts) < 2 {
			continue
		}
		relPath := parts[1]

		if !strings.HasPrefix(relPath, scriptSubpath) {
			continue
		}

		// Get the filename after the ta/ prefix
		name := strings.TrimPrefix(relPath, scriptSubpath)
		if name == "" || strings.Contains(name, "/") {
			continue // skip subdirectories and empty names
		}

		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		destPath := filepath.Join(destDir, name)
		f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			return count, fmt.Errorf("creating %s: %w", name, err)
		}

		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return count, fmt.Errorf("writing %s: %w", name, err)
		}
		f.Close()
		count++
	}

	return count, nil
}

// LastUpdated returns the time of the last script sync, or zero if never synced.
func LastUpdated() time.Time {
	home, _ := os.UserHomeDir()
	ts := filepath.Join(home, ".tatool", ".last-updated")
	data, err := os.ReadFile(ts)
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	if err != nil {
		return time.Time{}
	}
	return t
}
