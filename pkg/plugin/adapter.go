package plugin

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// AdapterAPIVersion is the version of CTX's adapter process protocol.
const AdapterAPIVersion = "2.0"

// AdapterContractName identifies CTX's argv/stream adapter binding. Its
// version is the adapter API, independently of the plugin envelope API.
const AdapterContractName = "ctx.adapter"

// AdapterInvocation is one operation delivered by ctx to an adapter
// executable.
type AdapterInvocation struct {
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

// ParseAdapterInvocation reads the versioned ctx process invocation. Call it
// with os.Args[1:]. It also accepts direct invocations without ctx
// environment variables, which makes an adapter executable easy to inspect
// during development.
func ParseAdapterInvocation(args []string) (AdapterInvocation, error) {
	if len(args) == 0 || args[0] == "" {
		return AdapterInvocation{}, errors.New("adapter operation is required")
	}
	request := AdapterInvocation{
		Operation:   args[0],
		API:         os.Getenv("CTX_ADAPTER_API"),
		Name:        os.Getenv("CTX_ADAPTER_NAME"),
		Command:     os.Getenv("CTX_ADAPTER_COMMAND"),
		RealCommand: os.Getenv("CTX_ADAPTER_REAL_COMMAND"),
		Project:     os.Getenv("CTX_PROJECT_DIR"),
		Profile:     os.Getenv("CTX_PROFILE"),
		Values:      map[string]string{},
	}
	if request.API != "" && request.API != AdapterAPIVersion {
		return AdapterInvocation{}, fmt.Errorf("unsupported ctx adapter API %s", request.API)
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
			return AdapterInvocation{}, fmt.Errorf("%s accepts no arguments", request.Operation)
		}
	case "validate", "doctor":
		if len(args) != 2 {
			return AdapterInvocation{}, fmt.Errorf("%s needs one selection", request.Operation)
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
			return AdapterInvocation{}, errors.New("adapter operation needs [selection] -- [arguments...]")
		}
		if separator == 2 {
			request.Selection = args[1]
		}
		request.Arguments = append([]string(nil), args[separator+1:]...)
	}
	return request, nil
}

// AdapterDescriptor translates a reviewed adapter package into the common
// contract. The caller supplies a content revision and declared operations.
// CLI adapters retain their argv, environment, exit status, and streams; this
// descriptor does not claim that they serve JSON-line plugin handshakes.
func AdapterDescriptor(identity Identity, operations []string) Descriptor {
	contract := Contract{ContractRef: ContractRef{Name: AdapterContractName, Version: AdapterAPIVersion}}
	seen := map[string]bool{}
	for _, name := range operations {
		if seen[name] {
			continue
		}
		seen[name] = true
		contract.Operations = append(contract.Operations, Operation{Name: name})
	}
	return Descriptor{APIVersion: APIVersion, Identity: identity, Contracts: []Contract{contract}}
}
