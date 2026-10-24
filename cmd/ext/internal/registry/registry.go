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

// Registry maps command names to the entry that runs them.
type Registry struct {
	commands map[string]Command
}

// New returns an empty registry.
func New() *Registry { return &Registry{commands: map[string]Command{}} }

// reserved names are ext's own subcommands; a command cannot take one.
var reserved = map[string]bool{"commands": true, "which": true, "version": true, "help": true}

// Register adds a command. A name is registered once: a second registration of the
// same name is an error, so a command never shadows another silently.
func (r *Registry) Register(c Command) error {
	if c.Name == "" || c.Run == nil {
		return fmt.Errorf("registry: a command needs a name and a Run function")
	}
	if reserved[c.Name] {
		return fmt.Errorf("registry: %q is an ext subcommand and cannot be a command name", c.Name)
	}
	if c.Source != Builtin && c.Source != Package {
		return fmt.Errorf("registry: command %q has unknown source %q", c.Name, c.Source)
	}
	if prev, ok := r.commands[c.Name]; ok {
		return fmt.Errorf("registry: command %q is already registered as %s", c.Name, prev.Source)
	}
	r.commands[c.Name] = c
	return nil
}

// Lookup returns the command registered under name.
func (r *Registry) Lookup(name string) (Command, bool) {
	c, ok := r.commands[name]
	return c, ok
}

// List returns every command ordered by name.
func (r *Registry) List() []Command {
	out := make([]Command, 0, len(r.commands))
	for _, c := range r.commands {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
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
