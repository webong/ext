package main

import (
	"context"
	"os"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	mlplugin "github.com/webong/ext/pkg/ext-go/ml"
	"github.com/webong/ext/res/ml"
)

type provider struct{}

func (provider) Name() string           { return "onnxruntime" }
func (provider) Capabilities() []string { return []string{"image.classify"} }
func (provider) Load(_ context.Context, model *ml.InstalledModel) (ml.Session, error) {
	if model.Manifest.Provider != "onnxruntime" {
		return nil, ml.ErrInvalid
	}
	return session{}, nil
}

type session struct{}

func (session) Infer(_ context.Context, inputs []ml.Tensor) ([]ml.Tensor, error) {
	return inputs, nil
}
func (session) Close() error { return nil }

func main() {
	if len(os.Args) != 2 || os.Args[1] != "run" {
		os.Exit(2)
	}
	auth := func(context.Context, plugin.Request) error { return nil }
	if err := mlplugin.ServeCurrentExecutable(context.Background(), "onnxruntime", "1.0.0", provider{}, auth); err != nil {
		os.Exit(1)
	}
}
