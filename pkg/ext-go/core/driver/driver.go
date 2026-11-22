// Package driver chooses how a library of lib/ runs in a program. It knows no library: a library's Go
// package lists its own drivers and asks Choose for one.
//
// A driver is one way of reaching the library's code:
//
//	cgo     the C library linked through cgo, fixed when the program is built
//	c2go    the C library translated to Go by ccgo, fixed when the program is built
//	purego  the C library as a shared library loaded when the program runs (cshared/library)
//	go      the library's pure-Go implementation, which exists on every platform
//
// Choose prefers them in that order. A program names one, or asks for the best available one with
// Auto; FromEnvironment reads the name from EXT_ENGINE. Asking for a driver by name never gets another
// one: a name that cannot be served is an error.
package driver

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Name identifies a driver.
type Name string

const (
	// CGo is the C library linked through cgo.
	CGo Name = "cgo"
	// C2Go is the C library translated to Go.
	C2Go Name = "c2go"
	// PureGo is the C library loaded at run time as a shared library.
	PureGo Name = "purego"
	// Go is the library's pure-Go implementation.
	Go Name = "go"
)

// Auto asks Choose for the best available driver.
const Auto Name = "auto"

// Environment is the variable FromEnvironment reads: a driver name, or auto (the default).
const Environment = "EXT_ENGINE"

var (
	// ErrUnknown is a name that is not a driver.
	ErrUnknown = errors.New("unknown driver")
	// ErrUnavailable is a driver this build or this machine cannot serve.
	ErrUnavailable = errors.New("driver unavailable")
)

// Order lists every driver in the order Choose prefers them.
func Order() []Name { return []Name{CGo, C2Go, PureGo, Go} }

// Parse reads a driver name. Empty is Auto; any other name that is not a driver is ErrUnknown.
func Parse(s string) (Name, error) {
	n := Name(strings.TrimSpace(s))
	if n == "" || n == Auto {
		return Auto, nil
	}
	for _, d := range Order() {
		if n == d {
			return n, nil
		}
	}
	return "", fmt.Errorf("driver: %q (want %s, %s, %s, %s or %s): %w", string(n), CGo, C2Go, PureGo, Go, Auto, ErrUnknown)
}

// FromEnvironment is Parse of EXT_ENGINE.
func FromEnvironment() (Name, error) { return Parse(os.Getenv(Environment)) }

// Driver is what a library's Go package lists for each way it can run.
type Driver interface {
	Name() Name
	// Available reports whether this build and this machine hold the driver.
	Available() bool
}

// Choose returns the driver called want from drivers, or with Auto (or an empty name) the first
// available one, in Order, for which fits returns nil. fits says why an available driver does not
// serve the caller, such as a missing feature; nil fits accepts every available driver. A named driver
// that is missing from drivers, unavailable or rejected by fits is an error, never another driver.
func Choose[D Driver](drivers []D, want Name, fits func(D) error) (D, error) {
	var zero D
	want, err := Parse(string(want))
	if err != nil {
		return zero, err
	}
	if fits == nil {
		fits = func(D) error { return nil }
	}
	if want == Auto {
		for _, name := range Order() {
			for _, d := range drivers {
				if d.Name() == name && d.Available() && fits(d) == nil {
					return d, nil
				}
			}
		}
		return zero, fmt.Errorf("driver: none here serves this: %w", ErrUnavailable)
	}
	for _, d := range drivers {
		if d.Name() != want {
			continue
		}
		if !d.Available() {
			return zero, fmt.Errorf("driver: %s is not available in this build: %w", want, ErrUnavailable)
		}
		if err := fits(d); err != nil {
			return zero, fmt.Errorf("driver: %s: %w: %w", want, err, ErrUnavailable)
		}
		return d, nil
	}
	return zero, fmt.Errorf("driver: %s is not a driver of this library: %w", want, ErrUnavailable)
}
