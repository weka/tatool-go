package runner

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"sync"

	"github.com/weka/tatool-go/internal/executor"
	"github.com/weka/tatool-go/internal/output"
	"github.com/weka/tatool-go/internal/script"
	"golang.org/x/sync/errgroup"
)

// TargetResult holds the results for one target (host or pod).
type TargetResult struct {
	Target  string
	Results []ScriptResult
}

// ScriptResult holds the result of one script on one target.
type ScriptResult struct {
	Script script.Script
	Result executor.ExecResult
	Err    error
}

// RunConfig holds all parameters for a run.
type RunConfig struct {
	Targets   []string
	Scripts   []script.Script
	ScriptsFS fs.FS
	Exec      executor.Executor
	Logger    *output.Logger
	UseDzdo   bool
}

// Run executes all scripts on all targets with goroutine-per-target parallelism.
func Run(ctx context.Context, cfg RunConfig) ([]TargetResult, error) {
	var (
		mu          sync.Mutex
		allResults  []TargetResult
	)

	// Separate scripts by type
	var parallel, single, compare []script.Script
	for _, s := range cfg.Scripts {
		switch s.Type {
		case script.Single:
			single = append(single, s)
		case script.ParallelCompareBackends:
			compare = append(compare, s)
		default:
			parallel = append(parallel, s)
		}
	}

	g, ctx := errgroup.WithContext(ctx)

	for i, target := range cfg.Targets {
		isFirst := i == 0

		g.Go(func() error {
			result := TargetResult{Target: target}

			// Copy scripts to target
			fmt.Fprintf(os.Stdout, "Copying scripts to %s...\n", target)
			if err := cfg.Exec.CopyScripts(ctx, target, cfg.ScriptsFS); err != nil {
				return fmt.Errorf("copy scripts to %s: %w", target, err)
			}
			cfg.Logger.Log(target, "Scripts copied")

			// Run parallel scripts on all targets
			for _, s := range parallel {
				r := runScript(ctx, cfg, target, s)
				result.Results = append(result.Results, r)
			}

			// Run single scripts only on first target
			if isFirst {
				for _, s := range single {
					r := runScript(ctx, cfg, target, s)
					result.Results = append(result.Results, r)
				}
			}

			// Run compare scripts on all targets (collected later)
			for _, s := range compare {
				r := runScript(ctx, cfg, target, s)
				result.Results = append(result.Results, r)
			}

			// Fetch diagnostics
			cfg.Exec.FetchDiagnostics(ctx, target, cfg.Logger.Dir())

			// Cleanup
			if err := cfg.Exec.Cleanup(ctx, target); err != nil {
				cfg.Logger.Log(target, fmt.Sprintf("Cleanup warning: %v", err))
			}
			cfg.Logger.Log(target, "Execution completed")

			mu.Lock()
			allResults = append(allResults, result)
			mu.Unlock()

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return allResults, err
	}

	// Check for discrepancies in compare scripts
	if len(compare) > 0 {
		reportDiscrepancies(allResults, compare)
	}

	return allResults, nil
}

func runScript(ctx context.Context, cfg RunConfig, target string, s script.Script) ScriptResult {
	cfg.Logger.Log(target, fmt.Sprintf("Running %s: %s", s.Filename, s.Description))

	result, err := cfg.Exec.Exec(ctx, target, s.Filename, cfg.UseDzdo)
	if err != nil {
		cfg.Logger.Log(target, fmt.Sprintf("ERROR %s: %v", s.Filename, err))
		output.PrintResult(fmt.Sprintf("[%s] %s", target, s.Filename), "FAIL")
		return ScriptResult{Script: s, Result: result, Err: err}
	}

	status := string(result.Status)
	cfg.Logger.Log(target, fmt.Sprintf("%s returned %s (code %d)", s.Filename, status, result.ExitCode))

	output.PrintStdout(result.Stdout)
	output.PrintStderr(result.Stderr)
	output.PrintResult(fmt.Sprintf("[%s] %s", target, s.Filename), status)

	return ScriptResult{Script: s, Result: result}
}

func reportDiscrepancies(results []TargetResult, compareScripts []script.Script) {
	for _, cs := range compareScripts {
		statusByTarget := make(map[string]string)
		for _, tr := range results {
			for _, sr := range tr.Results {
				if sr.Script.Filename == cs.Filename {
					statusByTarget[tr.Target] = string(sr.Result.Status)
				}
			}
		}

		// Check if all statuses match
		var first string
		discrepancy := false
		for _, status := range statusByTarget {
			if first == "" {
				first = status
			} else if status != first {
				discrepancy = true
				break
			}
		}

		if discrepancy {
			output.Red.Fprintf(os.Stdout, "\nDiscrepancy detected in %s:\n", cs.Filename)
			for target, status := range statusByTarget {
				fmt.Fprintf(os.Stdout, "  %s: %s\n", target, status)
			}
		}
	}
}

// PrintSummary prints a formatted table summary of results.
func PrintSummary(results []TargetResult) {
	if len(results) == 0 {
		return
	}

	// Calculate per-target stats and find max target name length
	type stats struct {
		target     string
		pass, fail, warn int
	}
	var rows []stats
	totalPass, totalFail, totalWarn := 0, 0, 0
	maxNameLen := 0

	// Also collect failed scripts
	failedScripts := make(map[string][]string) // script -> list of targets

	for _, tr := range results {
		s := stats{target: tr.Target}
		for _, sr := range tr.Results {
			switch sr.Result.Status {
			case executor.StatusPass:
				s.pass++
			case executor.StatusFail:
				s.fail++
				failedScripts[sr.Script.Filename] = append(failedScripts[sr.Script.Filename], tr.Target)
			case executor.StatusWarn:
				s.warn++
			}
		}
		totalPass += s.pass
		totalFail += s.fail
		totalWarn += s.warn
		if len(s.target) > maxNameLen {
			maxNameLen = len(s.target)
		}
		rows = append(rows, s)
	}

	// Clamp name length
	if maxNameLen < 6 {
		maxNameLen = 6
	}
	if maxNameLen > 60 {
		maxNameLen = 60
	}

	// Table drawing
	hLine := func(left, mid, right, fill string) string {
		return left + repeat(fill, maxNameLen+2) + mid + repeat(fill, 6) + mid + repeat(fill, 6) + mid + repeat(fill, 6) + right
	}

	fmt.Fprintln(os.Stdout)
	output.Bold.Fprintln(os.Stdout, "  Execution Summary")
	fmt.Fprintln(os.Stdout)

	// Header
	fmt.Fprintln(os.Stdout, "  "+hLine("┌", "┬", "┐", "─"))
	fmt.Fprintf(os.Stdout, "  │ %-*s │", maxNameLen, "Target")
	output.Green.Fprintf(os.Stdout, " PASS")
	fmt.Fprint(os.Stdout, " │")
	output.Red.Fprintf(os.Stdout, " FAIL")
	fmt.Fprint(os.Stdout, " │")
	output.Yellow.Fprintf(os.Stdout, " WARN")
	fmt.Fprintln(os.Stdout, " │")
	fmt.Fprintln(os.Stdout, "  "+hLine("├", "┼", "┤", "─"))

	// Rows
	for _, r := range rows {
		name := r.target
		if len(name) > maxNameLen {
			name = name[:maxNameLen-1] + "…"
		}
		fmt.Fprintf(os.Stdout, "  │ %-*s │", maxNameLen, name)
		output.Green.Fprintf(os.Stdout, " %4d", r.pass)
		fmt.Fprint(os.Stdout, " │")
		if r.fail > 0 {
			output.Red.Fprintf(os.Stdout, " %4d", r.fail)
		} else {
			fmt.Fprintf(os.Stdout, " %4d", r.fail)
		}
		fmt.Fprint(os.Stdout, " │")
		if r.warn > 0 {
			output.Yellow.Fprintf(os.Stdout, " %4d", r.warn)
		} else {
			fmt.Fprintf(os.Stdout, " %4d", r.warn)
		}
		fmt.Fprintln(os.Stdout, " │")
	}

	// Total row
	fmt.Fprintln(os.Stdout, "  "+hLine("├", "┼", "┤", "─"))
	totalLabel := fmt.Sprintf("TOTAL (%d targets)", len(rows))
	fmt.Fprintf(os.Stdout, "  │ %-*s │", maxNameLen, totalLabel)
	output.Green.Fprintf(os.Stdout, " %4d", totalPass)
	fmt.Fprint(os.Stdout, " │")
	if totalFail > 0 {
		output.Red.Fprintf(os.Stdout, " %4d", totalFail)
	} else {
		fmt.Fprintf(os.Stdout, " %4d", totalFail)
	}
	fmt.Fprint(os.Stdout, " │")
	if totalWarn > 0 {
		output.Yellow.Fprintf(os.Stdout, " %4d", totalWarn)
	} else {
		fmt.Fprintf(os.Stdout, " %4d", totalWarn)
	}
	fmt.Fprintln(os.Stdout, " │")
	fmt.Fprintln(os.Stdout, "  "+hLine("└", "┴", "┘", "─"))

	// List failed scripts
	if len(failedScripts) > 0 {
		fmt.Fprintln(os.Stdout)
		output.Red.Fprintln(os.Stdout, "  Failed Scripts:")
		for script, targets := range failedScripts {
			if len(targets) == len(rows) {
				fmt.Fprintf(os.Stdout, "    %s — all targets\n", script)
			} else {
				fmt.Fprintf(os.Stdout, "    %s — %d/%d targets\n", script, len(targets), len(rows))
			}
		}
	}
}

func repeat(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
