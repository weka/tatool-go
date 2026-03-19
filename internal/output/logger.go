package output

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Logger writes per-target log files.
type Logger struct {
	dir string
}

func NewLogger(dir string) (*Logger, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating log dir %s: %w", dir, err)
	}
	return &Logger{dir: dir}, nil
}

func (l *Logger) Log(target, msg string) error {
	path := filepath.Join(l.dir, fmt.Sprintf("ta_runner_%s.log", target))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	ts := time.Now().Format("2006-01-02 15:04:05")
	_, err = fmt.Fprintf(f, "[%s] %s\n", ts, msg)
	return err
}

func (l *Logger) Dir() string {
	return l.dir
}
