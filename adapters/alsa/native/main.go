package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/webong/ext/adapters/alsa"
	"github.com/webong/ext/pkg/ext-go/cgoengine"
	"github.com/webong/ext/res/system/audio"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	request, err := cgoengine.ParseAdapterInvocation(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "alsa: %v\n", err)
		return 2
	}
	switch request.Operation {
	case "validate":
		return 0
	case "doctor":
		for _, tool := range []string{"aplay", "arecord"} {
			if _, err := exec.LookPath(tool); err != nil {
				fmt.Fprintf(os.Stderr, "alsa: %s is unavailable: %v\n", tool, err)
				return 127
			}
		}
		return 0
	case "run":
		if len(request.Arguments) > 0 && request.Arguments[0] != "devices" {
			fmt.Fprintf(os.Stderr, "alsa: unsupported operation %s\n", request.Arguments[0])
			return 2
		}
		snapshot, err := alsa.NewObserver(nil).Observe(context.Background(), audio.Limits{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "alsa: %v\n", err)
			return 1
		}
		encoded, err := json.MarshalIndent(snapshot, "", "  ")
		if err != nil {
			return 1
		}
		fmt.Println(string(encoded))
		return 0
	default:
		fmt.Fprintf(os.Stderr, "alsa: unsupported adapter operation %s\n", request.Operation)
		return 2
	}
}
