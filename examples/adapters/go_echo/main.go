package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/webong/ctx/pkg/plugin"
)

func main() {
	request, err := plugin.ParseAdapterInvocation(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	switch request.Operation {
	case "list":
		fmt.Println("local")
	case "validate":
		if request.Selection != "local" {
			fmt.Fprintln(os.Stderr, "unknown context")
			os.Exit(1)
		}
	case "doctor":
		fmt.Println("Go echo adapter is ready")
	case "run":
		fmt.Printf("%s: %s\n", request.Selection, strings.Join(request.Arguments, " "))
	default:
		fmt.Fprintln(os.Stderr, "unsupported operation")
		os.Exit(2)
	}
}
