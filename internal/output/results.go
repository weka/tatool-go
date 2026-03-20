package output

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ResultEntry holds [exitCode, stdout, stderr] for one target.
type ResultEntry [3]interface{}

// ResultsJSON is the top-level structure: script_key -> target -> result.
type ResultsJSON map[string]map[string]ResultEntry

// WriteResultsData accepts the raw data already extracted by the caller and
// writes it to <dir>/test_results.json.
func WriteResultsData(dir string, data ResultsJSON) error {
	path := filepath.Join(dir, "test_results.json")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating test_results.json: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "    ")
	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("writing test_results.json: %w", err)
	}
	return nil
}
