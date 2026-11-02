package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/webong/ext/adapters/ffmpeg"
	"github.com/webong/ext/pkg/ext-go/cgoengine"
	"github.com/webong/ext/res/system/capture/media"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	request, err := cgoengine.ParseAdapterInvocation(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ffmpeg: %v\n", err)
		return 2
	}
	switch request.Operation {
	case "validate":
		return 0
	case "doctor":
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			fmt.Fprintf(os.Stderr, "ffmpeg: ffmpeg is unavailable: %v\n", err)
			return 127
		}
		return 0
	case "run":
		if len(request.Arguments) != 3 || request.Arguments[0] != "record" {
			fmt.Fprintln(os.Stderr, "ffmpeg: run supports: record <mp4|webm> <seconds>")
			return 2
		}
		seconds, err := strconv.Atoi(request.Arguments[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "ffmpeg: seconds: %v\n", err)
			return 2
		}
		recorder, err := ffmpeg.New(ffmpeg.Config{Source: os.Getenv("EXT_FFMPEG_SOURCE")})
		if err != nil {
			fmt.Fprintf(os.Stderr, "ffmpeg: %v\n", err)
			return 1
		}
		duration := time.Duration(seconds) * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), duration+10*time.Second)
		defer cancel()
		recording, err := recorder.Record(ctx, media.RecordingOptions{Format: request.Arguments[1], MaxDuration: duration})
		if err != nil {
			fmt.Fprintf(os.Stderr, "ffmpeg: %v\n", err)
			return 1
		}
		defer recording.Close()
		if _, err = io.Copy(os.Stdout, recording); err != nil {
			fmt.Fprintf(os.Stderr, "ffmpeg: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "ffmpeg: unsupported operation %q\n", request.Operation)
		return 2
	}
}
