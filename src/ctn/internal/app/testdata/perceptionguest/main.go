package main

import (
	"context"
	"os"
	"strconv"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	domain "github.com/webong/ext/res/int/perception"
	bridge "github.com/webong/ext/src/ctn/perception"
)

type fixture struct{}

func (fixture) Info(context.Context) (domain.Info, error) {
	return domain.Info{ID: "perception-fixture", Capabilities: []string{"text.interpret", "image.observe"}}, nil
}
func (fixture) Close() error { return nil }
func (fixture) Interpret(_ context.Context, request domain.InterpretRequest) (domain.InterpretResponse, error) {
	return domain.InterpretResponse{APIVersion: domain.APIVersion, RequestID: request.RequestID, Findings: []domain.Finding{{Kind: "summary", Text: request.Parts[0].Text, SourceParts: []int{0}}}}, nil
}
func (fixture) Observe(_ context.Context, request domain.ObserveRequest) (domain.ObserveResponse, error) {
	return domain.ObserveResponse{APIVersion: domain.APIVersion, RequestID: request.RequestID, Observations: []domain.Observation{{Kind: "object", Label: "fixture", Confidence: 1, Region: domain.Region{Width: 1, Height: 1}}}}, nil
}

func main() {
	if path := os.Getenv("EXT_TEST_PERCEPTION_PID"); path != "" {
		if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(1)
		}
	}

	err := bridge.ServeCurrentExecutable(context.Background(), "perception-fixture", "1.0.0", fixture{}, func(context.Context, plugin.Request) error { return nil })
	if err != nil {
		os.Exit(1)
	}
}
