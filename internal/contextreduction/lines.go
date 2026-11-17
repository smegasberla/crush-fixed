package contextreduction

import (
	"fmt"
	"strings"
)

// CollapseRepeatedLines shrinks tool output whose bulk is repetition. A build,
// test run or listing routinely emits the same line hundreds of times (one per
// file, one per dependency, one per retry), and every copy is re-sent on every
// later request.
//
// The reduction is information-preserving for the cases that matter: distinct
// lines are kept in order and verbatim, so a failure, a stack frame or a
// summary is never altered. Only runs of a line that repeats back to back are
// folded, and the count is written where they were, so nothing becomes
// invisible. It reports whether anything was folded.
func CollapseRepeatedLines(text string, minRun int) (string, bool) {
	if minRun < 2 {
		minRun = 2
	}
	lines := strings.Split(text, "\n")
	if len(lines) < minRun {
		return text, false
	}

	var out strings.Builder
	out.Grow(len(text))
	folded := false
	for i := 0; i < len(lines); {
		// Count the run of identical lines starting here. Blank lines are
		// left alone: folding them would change layout without saving much.
		run := 1
		if strings.TrimSpace(lines[i]) != "" {
			for i+run < len(lines) && lines[i+run] == lines[i] {
				run++
			}
		}
		if run >= minRun {
			fmt.Fprintf(&out, "%s\n... [%d identical lines] ...\n", lines[i], run)
			folded = true
			i += run
			continue
		}
		out.WriteString(lines[i])
		if i < len(lines)-1 {
			out.WriteString("\n")
		}
		i++
	}
	if !folded {
		return text, false
	}
	return out.String(), true
}
