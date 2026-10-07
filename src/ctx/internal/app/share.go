package app

import (
	"fmt"
	"io"
	"os"

	"github.com/webong/ext/ctx/internal/config"
	modpkg "github.com/webong/ext/ctx/internal/mod"
)

func shareSpaceCommand(resolver *config.Resolver, space string, args []string, stdout, stderr io.Writer) int {
	if space == "" {
		fmt.Fprintln(stderr, "ctx: share space is required")
		return 2
	}
	switch space {
	case "manager":
		if len(args) == 0 {
			fmt.Fprintln(stderr, "ctx: share:manager requires image or volume")
			return 2
		}
		switch args[0] {
		case "image":
			return imageCommand(resolver, args[1:], stdout, stderr)
		case "volume":
			return volumeCommand(resolver, args[1:], stdout, stderr)
		default:
			fmt.Fprintln(stderr, "ctx: share:manager requires image or volume")
			return 2
		}
	case "browser":
		return shareBrowserCommand(resolver, args, stdout, stderr)
	case "credential":
		return credentialCommand(resolver, args, os.Stdin, stdout, stderr)
	case "computer":
		fmt.Fprintln(stderr, "ctx: computer sharing is not available yet; no computer transfer format is registered")
		return 1
	default:
		store := adapterStore()
		installed, err := store.List()
		if err != nil {
			return reportError(stderr, err)
		}
		var matched *modpkg.Adapter
		var untrusted *modpkg.Adapter
		for _, candidate := range installed {
			if !candidate.HasCapability("share") || !containsShareSpace(candidate.Manifest.ShareSpaces, space) {
				continue
			}
			trusted, err := store.IsTrusted(candidate)
			if err != nil {
				return reportError(stderr, err)
			}
			if !trusted {
				untrusted = candidate
				continue
			}
			if matched != nil {
				fmt.Fprintf(stderr, "ctx: share space %s is registered by both %s and %s\n", space, matched.Manifest.Name, candidate.Manifest.Name)
				return 2
			}
			matched = candidate
		}
		if matched == nil {
			if untrusted != nil {
				fmt.Fprintf(stderr, "ctx: adapter %s offers share space %s but is not trusted; run ctx adapter trust %s after reviewing it\n", untrusted.Manifest.Name, space, untrusted.Manifest.Name)
				return 1
			}
			fmt.Fprintf(stderr, "ctx: no share space %s is registered\n", space)
			return 2
		}
		selection, err := adapterSelection(resolver, matched)
		if err != nil {
			return reportError(stderr, err)
		}
		inventory, err := freshMachineInventory(resolver)
		if err != nil {
			return reportError(stderr, err)
		}
		if _, err := inventory.Find(matched.Manifest.Name, selection); err == nil {
			if _, err := inventory.Find(matched.Manifest.Name, selection, "share"); err != nil {
				return reportError(stderr, err)
			}
		}
		return invokeAdapter(resolver, matched, "share", selection, append([]string{space}, args...), "", stdout, stderr)
	}
}

func containsShareSpace(spaces []string, wanted string) bool {
	for _, space := range spaces {
		if space == wanted {
			return true
		}
	}
	return false
}
