package selector

import (
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/weka/tatool-go/internal/script"
)

// InteractiveSelect presents a TUI multi-select for choosing scripts.
func InteractiveSelect(scripts []script.Script) ([]script.Script, error) {
	if len(scripts) == 0 {
		return nil, fmt.Errorf("no scripts available to select")
	}

	options := make([]huh.Option[int], len(scripts))
	for i, s := range scripts {
		label := fmt.Sprintf("%s: %s", s.Filename, s.Description)
		if s.Type != script.Parallel {
			label += fmt.Sprintf(" [%s]", s.Type)
		}
		options[i] = huh.NewOption(label, i)
	}

	var selected []int

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[int]().
				Title("Select scripts to run").
				Options(options...).
				Value(&selected).
				Height(20),
		),
	)

	if err := form.Run(); err != nil {
		return nil, fmt.Errorf("interactive selection: %w", err)
	}

	if len(selected) == 0 {
		return nil, fmt.Errorf("no scripts selected")
	}

	result := make([]script.Script, 0, len(selected))
	for _, idx := range selected {
		result = append(result, scripts[idx])
	}

	return result, nil
}
