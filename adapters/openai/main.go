// Package openai implements the inference.provider/v1 contract for the
// OpenAI API. It is an Ext plugin guest served over JSON-line stdio.
//
// Security properties:
//   - Explicit endpoint configuration (no convenient defaults)
//   - Redirects disabled by default
//   - Bounded request and response bodies
//   - Bounded streams with cancellation propagation
//   - No automatic retries
//   - No implicit credential discovery (credentials via explicit secret resolver)
//   - No credentials in errors, metadata, or logs
//   - Incomplete streams fail rather than becoming successful partial responses
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/webong/ext/adapters/openai/client"
	ginference "github.com/webong/ext/pkg/ext-go/inference"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "openai: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		return errors.New("adapter operation is required")
	}
	switch args[0] {
	case "run":
		return serve()
	case "validate":
		return nil
	case "doctor":
		return doctor()
	default:
		return fmt.Errorf("unsupported operation %q", args[0])
	}
}

func doctor() error {
	slog.Info("openai adapter: ready")
	return nil
}

func serve() error {
	ctx := context.Background()

	// The OpenAI provider is constructed with configuration from the
	// environment. Credentials are supplied through an explicit secret
	// resolver (EXT_ADAPTER_VALUE_api_key). No implicit discovery.
	apiKey := os.Getenv("EXT_ADAPTER_VALUE_api_key")
	endpoint := os.Getenv("EXT_ADAPTER_VALUE_endpoint")
	if endpoint == "" {
		return errors.New("endpoint is required (set EXT_ADAPTER_VALUE_endpoint)")
	}
	if apiKey == "" {
		return errors.New("api key is required (set EXT_ADAPTER_VALUE_api_key)")
	}

	provider, err := client.NewProvider(endpoint, apiKey)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}
	defer provider.Close()

	return ginference.ServeCurrentExecutable(ctx, "openai", "1.0.0", provider, authorize)
}

func authorize(ctx context.Context, req ginference.Request) error {
	if err := ctx.Err(); err != nil {
		return ctx.Err()
	}
	// Authorization is explicit: the caller must be admitted by the host.
	// The adapter does not perform implicit authorization.
	return nil
}
