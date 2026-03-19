package executor

import (
	"context"
	"io/fs"
)

// ResultStatus represents the outcome of a script execution.
type ResultStatus string

const (
	StatusPass  ResultStatus = "PASS"
	StatusFail  ResultStatus = "FAIL"
	StatusWarn  ResultStatus = "WARN"
	StatusFatal ResultStatus = "FATAL"
)

// ExecResult holds the output and status of a single script execution.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Status   ResultStatus
}

// StatusFromExitCode maps a script exit code to a ResultStatus.
func StatusFromExitCode(code int) ResultStatus {
	switch code {
	case 0:
		return StatusPass
	case 254:
		return StatusWarn
	case 255:
		return StatusFatal
	default:
		return StatusFail
	}
}

// Executor defines the interface for running scripts on remote targets.
type Executor interface {
	// CopyScripts deploys the script directory to the remote target.
	CopyScripts(ctx context.Context, target string, scripts fs.FS) error

	// Exec runs a single script on the target and returns the result.
	Exec(ctx context.Context, target string, scriptPath string, useDzdo bool) (ExecResult, error)

	// Cleanup removes deployed scripts from the target.
	Cleanup(ctx context.Context, target string) error

	// FetchDiagnostics collects /tmp/diagnostics* from the target to localDir.
	FetchDiagnostics(ctx context.Context, target string, localDir string) error

	// Close releases all resources (connections, etc).
	Close() error
}
