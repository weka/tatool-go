package runner

import (
	"fmt"

	"github.com/weka/tatool-go/internal/output"
)

// BuildResultsJSON converts runner results into the output.ResultsJSON format:
//
//	{ "filename:description": { "target": [exitCode, stdout, stderr] } }
//
// This matches the test_results.json format produced by the original ta-tool.
func BuildResultsJSON(results []TargetResult) output.ResultsJSON {
	data := make(output.ResultsJSON)

	for _, tr := range results {
		for _, sr := range tr.Results {
			key := fmt.Sprintf("%s:%s", sr.Script.Filename, sr.Script.Description)

			if _, ok := data[key]; !ok {
				data[key] = make(map[string]output.ResultEntry)
			}

			stderr := sr.Result.Stderr
			if sr.Err != nil {
				// Executor-level error (e.g. SSM SendCommand failed) — surface it
				// in the stderr slot so it shows up in the JSON.
				if stderr != "" {
					stderr += "\n"
				}
				stderr += sr.Err.Error()
			}

			data[key][tr.Target] = output.ResultEntry{
				sr.Result.ExitCode,
				sr.Result.Stdout,
				stderr,
			}
		}
	}

	return data
}
