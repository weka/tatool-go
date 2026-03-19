package script

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ResolveFS returns the script filesystem to use based on priority:
// 1. --scripts flag (explicit local dir)
// 2. ~/.tatool/scripts/ (cached from update-scripts)
// 3. Embedded fallback
func ResolveFS(scriptsFlag string) (fs.FS, string, error) {
	if scriptsFlag != "" {
		info, err := os.Stat(scriptsFlag)
		if err != nil {
			return nil, "", fmt.Errorf("scripts path %q: %w", scriptsFlag, err)
		}
		if !info.IsDir() {
			return nil, "", fmt.Errorf("scripts path %q is not a directory", scriptsFlag)
		}
		return os.DirFS(scriptsFlag), "local: " + scriptsFlag, nil
	}

	cacheDir := cachedScriptsDir()
	if info, err := os.Stat(cacheDir); err == nil && info.IsDir() {
		entries, err := os.ReadDir(cacheDir)
		if err == nil && len(entries) > 0 {
			return os.DirFS(cacheDir), "cached: " + cacheDir, nil
		}
	}

	embedded, err := EmbeddedFS()
	if err != nil {
		return nil, "", fmt.Errorf("loading embedded scripts: %w", err)
	}
	return embedded, "embedded", nil
}

func cachedScriptsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tatool", "scripts")
}
