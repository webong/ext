// Package adapter provides the public process protocol used by Go adapters.
// An adapter is still a separate executable; ctx never loads Go plugins.
package adapter

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

const APIVersion = "2.0"

// Invocation is one operation delivered by ctx to an adapter executable.
type Invocation struct {
	Operation   string
	Selection   string
	Arguments   []string
	API         string
	Name        string
	Command     string
	RealCommand string
	Project     string
	Profile     string
	Values      map[string]string
}

// Parse reads the versioned ctx process invocation. Call it with os.Args[1:].
// It also accepts direct invocations without ctx environment variables, which
// makes an adapter executable easy to inspect during development.
func Parse(args []string) (Invocation, error) {
	if len(args) == 0 || args[0] == "" {
		return Invocation{}, errors.New("adapter operation is required")
	}
	request := Invocation{
		Operation:   args[0],
		API:         os.Getenv("CTX_ADAPTER_API"),
		Name:        os.Getenv("CTX_ADAPTER_NAME"),
		Command:     os.Getenv("CTX_ADAPTER_COMMAND"),
		RealCommand: os.Getenv("CTX_ADAPTER_REAL_COMMAND"),
		Project:     os.Getenv("CTX_PROJECT_DIR"),
		Profile:     os.Getenv("CTX_PROFILE"),
		Values:      map[string]string{},
	}
	if request.API != "" && request.API != APIVersion {
		return Invocation{}, fmt.Errorf("unsupported ctx adapter API %s", request.API)
	}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok && strings.HasPrefix(key, "CTX_ADAPTER_VALUE_") {
			request.Values[strings.ToLower(strings.TrimPrefix(key, "CTX_ADAPTER_VALUE_"))] = value
		}
	}
	switch request.Operation {
	case "list", "observe":
		if len(args) != 1 {
			return Invocation{}, fmt.Errorf("%s accepts no arguments", request.Operation)
		}
	case "validate", "doctor":
		if len(args) != 2 {
			return Invocation{}, fmt.Errorf("%s needs one selection", request.Operation)
		}
		request.Selection = args[1]
	default:
		separator := -1
		for index := 1; index < len(args); index++ {
			if args[index] == "--" {
				separator = index
				break
			}
		}
		if separator < 1 || separator > 2 {
			return Invocation{}, errors.New("adapter operation needs [selection] -- [arguments...]")
		}
		if separator == 2 {
			request.Selection = args[1]
		}
		request.Arguments = append([]string(nil), args[separator+1:]...)
	}
	return request, nil
}
