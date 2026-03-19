package output

import (
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
)

var (
	Green  = color.New(color.FgGreen)
	Red    = color.New(color.FgRed)
	Yellow = color.New(color.FgYellow)
	Cyan   = color.New(color.FgCyan)
	Blue   = color.New(color.FgBlue)
	Bold   = color.New(color.Bold)
)

const (
	PassSym = "\u2705" // check mark
	FailSym = "\u274C" // cross mark
	WarnSym = "\u26A0\uFE0F"  // warning
)

// supportsUnicode does a basic check for unicode terminal support.
func supportsUnicode() bool {
	lang := os.Getenv("LANG")
	term := os.Getenv("TERM")
	return strings.Contains(strings.ToLower(lang), "utf") ||
		strings.Contains(strings.ToLower(term), "xterm")
}

func StatusSymbol(status string) string {
	if !supportsUnicode() {
		switch status {
		case "PASS":
			return "[PASS]"
		case "FAIL":
			return "[FAIL]"
		case "WARN":
			return "[WARN]"
		default:
			return "[" + status + "]"
		}
	}
	switch status {
	case "PASS":
		return PassSym
	case "FAIL":
		return FailSym
	case "WARN":
		return WarnSym
	default:
		return "[" + status + "]"
	}
}

func PrintResult(scriptName, status string) {
	sym := StatusSymbol(status)
	switch status {
	case "PASS":
		Green.Fprintf(os.Stdout, "%s %s: %s\n", sym, scriptName, status)
	case "FAIL":
		Red.Fprintf(os.Stdout, "%s %s: %s\n", sym, scriptName, status)
	case "WARN":
		Yellow.Fprintf(os.Stdout, "%s %s: %s\n", sym, scriptName, status)
	default:
		fmt.Fprintf(os.Stdout, "%s %s: %s\n", sym, scriptName, status)
	}
}

func PrintStdout(s string) {
	if s != "" {
		Blue.Fprint(os.Stdout, s)
		if !strings.HasSuffix(s, "\n") {
			fmt.Fprintln(os.Stdout)
		}
	}
}

func PrintStderr(s string) {
	if s != "" {
		Yellow.Fprint(os.Stderr, s)
		if !strings.HasSuffix(s, "\n") {
			fmt.Fprintln(os.Stderr)
		}
	}
}
