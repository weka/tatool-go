package runner

import (
	"context"
	"io/fs"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/weka/tatool-go/internal/executor"
	"github.com/weka/tatool-go/internal/output"
	"github.com/weka/tatool-go/internal/script"
)

// mockExecutor records calls and returns configurable results.
type mockExecutor struct {
	mu       sync.Mutex
	copies   []string
	execs    []execCall
	cleanups []string
}

type execCall struct {
	Target string
	Script string
}

func (m *mockExecutor) CopyScripts(_ context.Context, target string, _ fs.FS) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.copies = append(m.copies, target)
	return nil
}

func (m *mockExecutor) Exec(_ context.Context, target string, scriptPath string, _ bool) (executor.ExecResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.execs = append(m.execs, execCall{Target: target, Script: scriptPath})
	return executor.ExecResult{ExitCode: 0, Status: executor.StatusPass}, nil
}

func (m *mockExecutor) Cleanup(_ context.Context, target string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanups = append(m.cleanups, target)
	return nil
}

func (m *mockExecutor) FetchDiagnostics(_ context.Context, _ string, _ string) error {
	return nil
}

func (m *mockExecutor) Close() error { return nil }

func TestRunParallelScripts(t *testing.T) {
	mock := &mockExecutor{}
	logger, _ := output.NewLogger(t.TempDir())
	scriptsFS := fstest.MapFS{
		"001_test.sh": &fstest.MapFile{Data: []byte("#!/bin/bash\nexit 0\n")},
	}

	scripts := []script.Script{
		{Filename: "001_test.sh", Number: 1, Type: script.Parallel, Description: "test"},
	}

	results, err := Run(context.Background(), RunConfig{
		Targets:   []string{"pod1", "pod2"},
		Scripts:   scripts,
		ScriptsFS: scriptsFS,
		Exec:      mock,
		Logger:    logger,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 target results, got %d", len(results))
	}

	// Both targets should have copies
	if len(mock.copies) != 2 {
		t.Errorf("expected 2 copies, got %d", len(mock.copies))
	}

	// Both targets should have executed the script
	if len(mock.execs) != 2 {
		t.Errorf("expected 2 execs, got %d", len(mock.execs))
	}
}

func TestRunSingleScript(t *testing.T) {
	mock := &mockExecutor{}
	logger, _ := output.NewLogger(t.TempDir())
	scriptsFS := fstest.MapFS{
		"010_single.sh": &fstest.MapFile{Data: []byte("#!/bin/bash\nexit 0\n")},
	}

	scripts := []script.Script{
		{Filename: "010_single.sh", Number: 10, Type: script.Single, Description: "single test"},
	}

	results, err := Run(context.Background(), RunConfig{
		Targets:   []string{"pod1", "pod2", "pod3"},
		Scripts:   scripts,
		ScriptsFS: scriptsFS,
		Exec:      mock,
		Logger:    logger,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Only first target should have run the single script
	execCount := 0
	for _, tr := range results {
		execCount += len(tr.Results)
	}
	if execCount != 1 {
		t.Errorf("expected 1 single-script exec, got %d", execCount)
	}
}
