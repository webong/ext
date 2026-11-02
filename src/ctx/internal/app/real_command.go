package app

import "github.com/webong/ext/ctx/internal/launch"

// locateRealCommand finds the native executable behind the command a user typed.
// The typed name is tried first, then the other commands the adapter declares, so
// `ctx run python` works where only `python3` exists. When none is on PATH and the
// adapter finds its own programs (it was given discovered candidates), the lookup
// is not an error: the adapter chooses what to run, from copies that need not be on
// PATH, and exits 127 itself when it has nothing usable. Without discovery the
// original error stands. An empty path with no error means the adapter decides.
func locateRealCommand(requested string, declared []string, discovers bool) (string, error) {
	path, firstErr := launch.FindReal(requested)
	if firstErr == nil {
		return path, nil
	}
	tried := map[string]bool{requested: true}
	for _, name := range declared {
		if name == "" || tried[name] {
			continue
		}
		tried[name] = true
		if path, err := launch.FindReal(name); err == nil {
			return path, nil
		}
	}
	if discovers {
		return "", nil
	}
	return "", firstErr
}
