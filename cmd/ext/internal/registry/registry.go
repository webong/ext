// Package registry records the commands ext can run and how each one was loaded.
package registry

import (
	"fmt"
	"io"
	"sort"
)

// Source says how a command was loaded.
type Source string

const (
	// Builtin commands are compiled into ext and run in process.
	Builtin Source = "builtin"
	// Package commands are installed, trusted ext packages run as guests.
	Package Source = "package"
)

// Command is one entry of the registry.
type Command struct {
	Name    string
	Source  Source
	Version string
	Run     func(args []string, stdout, stderr io.Writer) int
}

// reserved names are ext's own subcommands; a command cannot take one.
var reserved = map[string]bool{"commands": true, "which": true, "version": true, "help": true}

// Choice says why a command's entry was selected.
type Choice string

const (
	ByDefault Choice = "default" // the builtin wins over a package of the same name
	ByPin     Choice = "pin"     // a standing choice for this command in the ext config
	ByFlag    Choice = "flag"    // --source for this invocation
)

// Registry maps command names to the entries that can run them. A name may be
// registered once per source, so a package that declares a name a builtin already
// has is kept beside it, never silently replacing it.
type Registry struct {
	commands map[string][]Command
	pins     map[string]Source
	notes    []string
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{commands: map[string][]Command{}, pins: map[string]Source{}}
}

// Register adds a command. A name is registered once per source; registering it
// again for the same source is an error.
func (r *Registry) Register(c Command) error {
	if c.Name == "" || c.Run == nil {
		return fmt.Errorf("registry: a command needs a name and a Run function")
	}
	if reserved[c.Name] {
		return fmt.Errorf("registry: %q is an ext subcommand and cannot be a command name", c.Name)
	}
	if !validSource(c.Source) {
		return fmt.Errorf("registry: command %q has unknown source %q", c.Name, c.Source)
	}
	for _, prev := range r.commands[c.Name] {
		if prev.Source == c.Source {
			return fmt.Errorf("registry: command %q is already registered as %s", c.Name, prev.Source)
		}
	}
	r.commands[c.Name] = append(r.commands[c.Name], c)
	return nil
}

func validSource(s Source) bool { return s == Builtin || s == Package }

// Note records something discovery skipped or refused, so that ext commands can say
// why a command is missing instead of leaving it out silently.
func (r *Registry) Note(text string) { r.notes = append(r.notes, text) }

// Notes returns what Note recorded, in order.
func (r *Registry) Notes() []string { return append([]string(nil), r.notes...) }

// Pin records the standing source for one command. It does not require the
// command to be registered yet, because packages are discovered after the config
// is read; a pin to a source the command lacks fails when the command is resolved.
func (r *Registry) Pin(name string, source Source) error {
	if !validSource(source) {
		return fmt.Errorf("registry: cannot pin %q to unknown source %q", name, source)
	}
	r.pins[name] = source
	return nil
}

// Resolve picks the entry that runs name. A requested source (the --source flag)
// wins over a pin, and a pin wins over the default, which prefers the builtin. A
// chosen source the command does not have is an error, never a fallback.
func (r *Registry) Resolve(name string, requested Source) (Command, Choice, error) {
	entries := r.commands[name]
	if len(entries) == 0 {
		return Command{}, "", fmt.Errorf("%w: %s", ErrUnknown, name)
	}
	pick := func(source Source) (Command, bool) {
		for _, c := range entries {
			if c.Source == source {
				return c, true
			}
		}
		return Command{}, false
	}
	if requested != "" {
		if !validSource(requested) {
			return Command{}, "", fmt.Errorf("registry: unknown source %q", requested)
		}
		c, ok := pick(requested)
		if !ok {
			return Command{}, "", fmt.Errorf("registry: command %q has no %s source", name, requested)
		}
		return c, ByFlag, nil
	}
	if pinned, ok := r.pins[name]; ok {
		c, ok := pick(pinned)
		if !ok {
			return Command{}, "", fmt.Errorf("registry: command %q is pinned to %s but has no such source", name, pinned)
		}
		return c, ByPin, nil
	}
	if c, ok := pick(Builtin); ok {
		return c, ByDefault, nil
	}
	return entries[0], ByDefault, nil
}

// ErrUnknown is returned by Resolve for a name nothing registered.
var ErrUnknown = fmt.Errorf("registry: unknown command")

// Entry is one registered command and whether it is the one that runs by default
// (no flag), after pins.
type Entry struct {
	Command
	Active bool
}

// List returns every entry ordered by name, then source, marking the one a plain
// invocation runs.
func (r *Registry) List() []Entry {
	var out []Entry
	for name, entries := range r.commands {
		active, _, err := r.Resolve(name, "")
		for _, c := range entries {
			out = append(out, Entry{Command: c, Active: err == nil && c.Source == active.Source})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Source < out[j].Source
	})
	return out
}

// Closest returns the registered name nearest to name, if one is close enough to be
// a likely typo.
func (r *Registry) Closest(name string) (string, bool) {
	best, bestDist := "", len(name)/2+2
	for n := range r.commands {
		if d := distance(name, n); d < bestDist || (d == bestDist && best != "" && n < best) {
			best, bestDist = n, d
		}
	}
	return best, best != ""
}

// distance is the edit distance between a and b where swapping two adjacent
// characters counts as one edit, so "cxt" is one edit from "ctx".
func distance(a, b string) int {
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
		d[i][0] = i
	}
	for j := 0; j <= len(b); j++ {
		d[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(a)][len(b)]
}
