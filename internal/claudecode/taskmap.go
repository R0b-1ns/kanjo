package claudecode

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/R0b-1ns/kanjo/internal/core"
)

// ReadTaskMap parses "<session id prefix><TAB><task>" lines. Blank lines and
// lines starting with "#" are ignored. Errors name the line number only.
func ReadTaskMap(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		prefix, task, err := parseTaskMapLine(line)
		if err == nil && out[prefix] != "" {
			err = fmt.Errorf("duplicate session prefix")
		}
		if err != nil {
			return nil, fmt.Errorf("task map line %d: %w", n, err)
		}
		out[prefix] = task
	}
	return out, sc.Err()
}

func parseTaskMapLine(line string) (prefix, task string, err error) {
	prefix, task, ok := strings.Cut(line, "\t")
	switch {
	case !ok:
		return "", "", fmt.Errorf("want <session prefix><TAB><task>")
	case !core.ValidSession(prefix):
		return "", "", fmt.Errorf("invalid session prefix")
	case !core.ValidTask(task) || task == core.NoTask:
		return "", "", fmt.Errorf("invalid task label")
	}
	return prefix, task, nil
}
