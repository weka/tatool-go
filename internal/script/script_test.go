package script

import (
	"testing"
	"testing/fstest"
)

func TestLoadAll(t *testing.T) {
	fs := fstest.MapFS{
		"010_ping.sh": &fstest.MapFile{
			Data: []byte(`#!/bin/bash
DESCRIPTION="Check network connectivity"
SCRIPT_TYPE="single"
JIRA_REFERENCE="WEKAPP-12345"
echo "hello"
`),
		},
		"001_collect_diags.sh": &fstest.MapFile{
			Data: []byte(`#!/bin/bash
DESCRIPTION="Collect diagnostic logs"
SCRIPT_TYPE="parallel"
echo "collecting"
`),
		},
		"260_compare_gateways.sh": &fstest.MapFile{
			Data: []byte(`#!/bin/bash
DESCRIPTION="Compare DPDK gateways"
SCRIPT_TYPE="parallel-compare-backends"
`),
		},
		"preamble": &fstest.MapFile{
			Data: []byte("# shared preamble\n"),
		},
		"README.md": &fstest.MapFile{
			Data: []byte("# docs\n"),
		},
	}

	scripts, err := LoadAll(fs)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	if len(scripts) != 3 {
		t.Fatalf("expected 3 scripts, got %d", len(scripts))
	}

	// Should be sorted by number
	if scripts[0].Number != 1 {
		t.Errorf("first script number = %d, want 1", scripts[0].Number)
	}
	if scripts[1].Number != 10 {
		t.Errorf("second script number = %d, want 10", scripts[1].Number)
	}
	if scripts[2].Number != 260 {
		t.Errorf("third script number = %d, want 260", scripts[2].Number)
	}

	// Check metadata
	if scripts[0].Description != "Collect diagnostic logs" {
		t.Errorf("script 001 description = %q", scripts[0].Description)
	}
	if scripts[1].Type != Single {
		t.Errorf("script 010 type = %q, want single", scripts[1].Type)
	}
	if scripts[1].JiraRef != "WEKAPP-12345" {
		t.Errorf("script 010 jira ref = %q", scripts[1].JiraRef)
	}
	if scripts[2].Type != ParallelCompareBackends {
		t.Errorf("script 260 type = %q, want parallel-compare-backends", scripts[2].Type)
	}
}

func TestLoadAllWithEmbedded(t *testing.T) {
	fsys, err := EmbeddedFS()
	if err != nil {
		t.Fatalf("EmbeddedFS: %v", err)
	}

	scripts, err := LoadAll(fsys)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	if len(scripts) < 50 {
		t.Errorf("expected at least 50 embedded scripts, got %d", len(scripts))
	}

	// Verify they are sorted
	for i := 1; i < len(scripts); i++ {
		if scripts[i].Number < scripts[i-1].Number {
			t.Errorf("scripts not sorted: %d before %d", scripts[i-1].Number, scripts[i].Number)
		}
	}

	// Every script should have a description
	for _, s := range scripts {
		if s.Description == "" {
			t.Errorf("script %s has empty description", s.Filename)
		}
	}
}

func TestFilterByNumbers(t *testing.T) {
	scripts := []Script{
		{Filename: "001_test.sh", Number: 1},
		{Filename: "010_test.sh", Number: 10},
		{Filename: "100_test.sh", Number: 100},
	}

	filtered := FilterByNumbers(scripts, []int{1, 100})
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered scripts, got %d", len(filtered))
	}
	if filtered[0].Number != 1 || filtered[1].Number != 100 {
		t.Error("wrong scripts filtered")
	}
}
