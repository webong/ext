package recorder

import (
	"os"
	"runtime"
	"time"

	"github.com/webong/ext/res/system/service/host"
)

// environment is everything the recorder reads from the machine besides the
// observations it is given. A Graph without one reads the machine; the
// recorder fixtures (testdata/recorder/v1) give it one, so the projection is a
// function of its inputs that any implementation can be held to.
type environment struct {
	now             func() time.Time
	hostname        func() string
	goos, goarch    string
	parentPID       func() int
	workingDir      func() string
	getenv          func(string) string
	exists          func(path string) bool
	hostEnvironment func() string
}

var machineEnvironment = environment{
	now:      time.Now,
	hostname: func() string { name, _ := os.Hostname(); return name },
	goos:     runtime.GOOS, goarch: runtime.GOARCH,
	parentPID:  os.Getppid,
	workingDir: func() string { dir, _ := os.Getwd(); return dir },
	getenv:     os.Getenv,
	exists: func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	},
	hostEnvironment: host.Environment,
}

func (g *Graph) environment() environment {
	if g.env != nil {
		return *g.env
	}
	return machineEnvironment
}
