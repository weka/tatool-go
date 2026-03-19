package output

import (
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Progress displays a compact spinner + checkmark progress for script execution.
type Progress struct {
	mu           sync.Mutex
	total        int
	completed    int
	currentLabel string // what's currently running (shown on spinner line)
	frame        int
	isTTY        bool
	stop         chan struct{}
	done         chan struct{}
}

// NewProgress creates a progress display. If stdout is not a TTY, spinner is disabled.
func NewProgress(totalScripts int) *Progress {
	p := &Progress{
		total: totalScripts,
		isTTY: term.IsTerminal(int(os.Stdout.Fd())),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	if p.isTTY {
		go p.spin()
	}
	return p
}

func (p *Progress) spin() {
	defer close(p.done)
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.mu.Lock()
			if p.currentLabel != "" {
				p.clearLine()
				spinner := spinnerFrames[p.frame%len(spinnerFrames)]
				fmt.Fprintf(os.Stdout, "%s Running scripts... [%d/%d] %s",
					spinner, p.completed, p.total, p.currentLabel)
				p.frame++
			}
			p.mu.Unlock()
		}
	}
}

func (p *Progress) clearLine() {
	fmt.Fprint(os.Stdout, "\r\033[K")
}

// Start marks a script as in-progress on the given target.
func (p *Progress) Start(target, scriptName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.currentLabel = fmt.Sprintf("[%s] %s", target, scriptName)
	if !p.isTTY {
		return
	}
	// spinner goroutine will pick it up
}

// Finish marks a script as completed, prints the result line, and advances the counter.
func (p *Progress) Finish(target, scriptName, status string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.completed++

	sym := StatusSymbol(status)

	if p.isTTY {
		p.clearLine()
	}

	label := fmt.Sprintf("[%d/%d] [%s] %s", p.completed, p.total, target, scriptName)
	switch status {
	case "PASS":
		Green.Fprintf(os.Stdout, "%s %s\n", sym, label)
	case "FAIL":
		Red.Fprintf(os.Stdout, "%s %s\n", sym, label)
	case "WARN":
		Yellow.Fprintf(os.Stdout, "%s %s\n", sym, label)
	default:
		fmt.Fprintf(os.Stdout, "%s %s\n", sym, label)
	}
}

// Stop halts the spinner goroutine. Call after all scripts complete.
func (p *Progress) Stop() {
	if p.isTTY {
		close(p.stop)
		<-p.done
		p.mu.Lock()
		p.clearLine()
		p.mu.Unlock()
	}
}
