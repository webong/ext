package main

import (
	"context"
	"os"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	bridge "github.com/webong/ext/pkg/ext-go/perception"
	domain "github.com/webong/ext/res/int/perception"
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
	err := bridge.ServeCurrentExecutable(context.Background(), "perception-fixture", "1.0.0", fixture{}, func(context.Context, plugin.Request) error { return nil })
	if err != nil {
		os.Exit(1)
	}
}
