package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/webong/ext/adapters/x11"
	"github.com/webong/ext/pkg/ext-go/cgoengine"
	"github.com/webong/ext/res/system"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	request, err := cgoengine.ParseAdapterInvocation(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "x11: %v\n", err)
		return 2
	}
	switch request.Operation {
	case "validate":
		return 0
	case "doctor":
		for _, tool := range []string{"import", "xdotool", "ffmpeg"} {
			if _, err := exec.LookPath(tool); err != nil {
				fmt.Fprintf(os.Stderr, "x11: %s is unavailable: %v\n", tool, err)
				return 127
			}
		}
		return 0
	case "run":
		if len(request.Arguments) != 1 || request.Arguments[0] != "capture" {
			fmt.Fprintln(os.Stderr, "x11: run supports capture")
			return 2
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		capture, err := system.CaptureDesktop(ctx, x11.New(x11.Config{}), system.CaptureOptions{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "x11: %v\n", err)
			return 1
		}
		if _, err = os.Stdout.Write(capture.Data); err != nil {
			return 1
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "x11: unsupported adapter operation %s\n", request.Operation)
		return 2
	}
}
