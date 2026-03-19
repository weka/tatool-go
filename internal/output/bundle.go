package output

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BundleLogs creates a compressed archive of all ta_runner_*.log files in logDir.
func BundleLogs(logDir, compression string) (string, error) {
	ts := time.Now().Format("20060102_150405")

	ext := "tar.gz"
	if compression == "bz2" {
		ext = "tar.gz" // Go stdlib only has gzip writer; keep gz for now
	}

	bundleName := fmt.Sprintf("ta_runner_logs_%s.%s", ts, ext)
	bundlePath := filepath.Join(logDir, bundleName)

	outFile, err := os.Create(bundlePath)
	if err != nil {
		return "", fmt.Errorf("creating bundle: %w", err)
	}
	defer outFile.Close()

	gw := gzip.NewWriter(outFile)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	entries, err := os.ReadDir(logDir)
	if err != nil {
		return "", fmt.Errorf("reading log dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "ta_runner_") || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}

		path := filepath.Join(logDir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			continue
		}
		hdr.Name = e.Name()

		if err := tw.WriteHeader(hdr); err != nil {
			return "", fmt.Errorf("writing tar header: %w", err)
		}

		f, err := os.Open(path)
		if err != nil {
			continue
		}
		if _, err := io.Copy(tw, f); err != nil {
			f.Close()
			return "", fmt.Errorf("writing tar content: %w", err)
		}
		f.Close()
	}

	return bundlePath, nil
}
