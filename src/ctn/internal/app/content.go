package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/webong/ext/ctn/internal/content"
)

// contentCommand exposes the portable local catalog without selecting a root
// or content policy for the caller.
func contentCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctn: content needs init, import, list, inspect, verify, or remove")
		return 2
	}
	if args[0] == "init" {
		if len(args) != 2 {
			fmt.Fprintln(stderr, "ctn: content init needs ROOT")
			return 2
		}
		catalog, err := content.InitCatalog(args[1])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		defer catalog.Close()
		fmt.Fprintln(stdout, catalog.Path())
		return 0
	}
	if len(args) < 2 {
		fmt.Fprintf(stderr, "ctn: content %s needs ROOT\n", args[0])
		return 2
	}
	catalog, err := content.OpenCatalog(args[1])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer catalog.Close()
	ctx := context.Background()
	switch args[0] {
	case "import":
		if len(args) != 3 {
			fmt.Fprintln(stderr, "ctn: content import needs ROOT FILE")
			return 2
		}
		item, err := catalog.Import(ctx, args[2])
		return contentResult(stdout, stderr, item, err)
	case "list":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "ctn: content list needs ROOT")
			return 2
		}
		items, err := catalog.List(ctx)
		return contentResult(stdout, stderr, items, err)
	case "inspect", "verify":
		if len(args) != 3 {
			fmt.Fprintf(stderr, "ctn: content %s needs ROOT ID\n", args[0])
			return 2
		}
		item, err := catalog.Inspect(ctx, args[2])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if args[0] == "verify" {
			fmt.Fprintf(stdout, "%s\t%s\n", item.ID, item.SHA256)
			return 0
		}
		return contentResult(stdout, stderr, item, nil)
	case "remove":
		if len(args) != 3 {
			fmt.Fprintln(stderr, "ctn: content remove needs ROOT ID")
			return 2
		}
		if err := catalog.Remove(args[2]); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, args[2])
		return 0
	default:
		fmt.Fprintf(stderr, "ctn: unknown content action %s\n", args[0])
		return 2
	}
}

func contentResult(stdout, stderr io.Writer, value any, err error) int {
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(value); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
