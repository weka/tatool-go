package script

import (
	"bufio"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type ScriptType string

const (
	Parallel              ScriptType = "parallel"
	Single                ScriptType = "single"
	ParallelCompareBackends ScriptType = "parallel-compare-backends"
)

type Script struct {
	Filename    string
	Number      int
	Description string
	Type        ScriptType
	JiraRef     string
	KBRef       string
}

var (
	scriptFileRe = regexp.MustCompile(`^(\d{1,4})[-_].*\.(sh|py)$`)
	descRe       = regexp.MustCompile(`DESCRIPTION\s*=\s*["'](.+?)["']`)
	typeRe       = regexp.MustCompile(`SCRIPT_TYPE\s*=\s*["'](.+?)["']`)
	jiraRe       = regexp.MustCompile(`JIRA_REFERENCE\s*=\s*["'](.+?)["']`)
	kbRe         = regexp.MustCompile(`KB_REFERENCE\s*=\s*["'](.+?)["']`)
)

// LoadAll reads scripts from an fs.FS, parses metadata, and returns them sorted by number.
func LoadAll(fsys fs.FS) ([]Script, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("reading scripts directory: %w", err)
	}

	var scripts []Script
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		m := scriptFileRe.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		num, _ := strconv.Atoi(m[1])

		s := Script{
			Filename: name,
			Number:   num,
			Type:     Parallel, // default
		}

		if err := parseMetadata(fsys, name, &s); err != nil {
			s.Description = "(error reading metadata)"
		}

		scripts = append(scripts, s)
	}

	sort.Slice(scripts, func(i, j int) bool {
		return scripts[i].Number < scripts[j].Number
	})

	return scripts, nil
}

func parseMetadata(fsys fs.FS, name string, s *Script) error {
	f, err := fsys.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for i := 0; i < 10 && scanner.Scan(); i++ {
		line := scanner.Text()

		if m := descRe.FindStringSubmatch(line); m != nil {
			s.Description = m[1]
		}
		if m := typeRe.FindStringSubmatch(line); m != nil {
			t := ScriptType(strings.TrimSpace(strings.ToLower(m[1])))
			switch t {
			case Parallel, Single, ParallelCompareBackends:
				s.Type = t
			}
		}
		if m := jiraRe.FindStringSubmatch(line); m != nil {
			s.JiraRef = m[1]
		}
		if m := kbRe.FindStringSubmatch(line); m != nil {
			s.KBRef = m[1]
		}
	}

	if s.Description == "" {
		s.Description = "(no description)"
	}

	return scanner.Err()
}

// FilterByNumbers returns only scripts whose number matches one of the given nums.
func FilterByNumbers(scripts []Script, nums []int) []Script {
	set := make(map[int]bool, len(nums))
	for _, n := range nums {
		set[n] = true
	}
	var filtered []Script
	for _, s := range scripts {
		if set[s.Number] {
			filtered = append(filtered, s)
		}
	}
	return filtered
}
