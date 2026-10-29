// Package app implements the ctn command. This file adds inference-resource
// commands that discover and invoke installed inference providers through the
// Ext plugin engine.
package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/process"
	ginference "github.com/webong/ext/pkg/ext-go/inference"
	inference "github.com/webong/ext/res/inference"
)

// inferenceCommand lists providers and runs inference operations through an
// explicitly selected provider plugin.
func inferenceCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctn: inference needs one of providers, capabilities, health, generate, stream, embed, transcribe, synthesize, or models")
		return 2
	}
	switch args[0] {
	case "providers":
		return inferenceProvidersCommand(args[1:], stdout, stderr)
	case "capabilities":
		return inferenceCapabilitiesCommand(args[1:], stdout, stderr)
	case "health":
		return inferenceHealthCommand(args[1:], stdout, stderr)
	case "generate":
		return inferenceGenerateCommand(args[1:], stdout, stderr)
	case "stream":
		return inferenceStreamCommand(args[1:], stdout, stderr)
	case "embed":
		return inferenceEmbedCommand(args[1:], stdout, stderr)
	case "transcribe":
		return inferenceTranscribeCommand(args[1:], stdout, stderr)
	case "synthesize":
		return inferenceSynthesizeCommand(args[1:], stdout, stderr)
	case "models":
		return inferenceModelsCommand(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "ctn: unknown inference action %s\n", args[0])
		return 2
	}
}

// providerConfig describes how to connect to an inference provider.
type providerConfig struct {
	ID    string // provider identity
	Path  string // executable path (for local plugins)
	Args  []string // extra arguments
}

// checkError converts an error to a ctn exit code, printing the error to stderr.
func checkError(err error) int {
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctn: %v\n", err)
		return 1
	}
	return 0
}

// resolveProvider resolves a provider by ID. The provider ID maps to an
// installed inference-provider package executable. For now, the provider path
// is taken from the EXT_INFERENCE_PROVIDER_<ID> environment variable.
func resolveProvider(id string) (providerConfig, error) {
	if id == "" {
		return providerConfig{}, fmt.Errorf("%w: provider ID is required", inference.ErrInvalidRequest)
	}
	envKey := "EXT_INFERENCE_PROVIDER_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_"))
	path := os.Getenv(envKey)
	if path == "" {
		// Fall back to looking up the provider as a direct executable path.
		if _, err := os.Stat(id); err == nil {
			return providerConfig{ID: id, Path: id}, nil
		}
		return providerConfig{}, fmt.Errorf("%w: provider %q not found (set %s)", inference.ErrUnavailable, id, envKey)
	}
	return providerConfig{ID: id, Path: path}, nil
}

// openProviderSession starts the provider plugin executable and opens a plugin
// session to it. The caller must close the returned session.
func openProviderSession(ctx context.Context, cfg providerConfig) (*plugin.Session, error) {
	cmd := exec.CommandContext(ctx, cfg.Path, "run")
	cmd.Stderr = os.Stderr
	backend, err := process.Start(ctx, process.Command{
		Path:  cmd.Path,
		Args:  cmd.Args[1:],
		Env:   os.Environ(),
	})
	if err != nil {
		return nil, fmt.Errorf("start provider %q: %w", cfg.ID, err)
	}

	// The provider serves inference.provider/v1. Open a session to it.
	descriptor := ginference.Descriptor(plugin.Identity{ID: cfg.ID, Revision: "", Version: "1.0.0"})
	session, err := plugin.Open(ctx, descriptor, plugin.Options{
		Verify: func(ctx context.Context, d plugin.Descriptor) error {
			// Verify the provider declares the inference contract.
			for _, c := range d.Contracts {
				if c.Name == ginference.ContractName && c.Version == ginference.ContractVersion {
					return nil
				}
			}
			return fmt.Errorf("%w: provider does not declare inference.provider/v1", inference.ErrUnavailable)
		},
		Connect: func(ctx context.Context, d plugin.Descriptor) (plugin.Backend, error) {
			return backend, nil
		},
		Authorize: func(ctx context.Context, req plugin.Request) error {
			return ctx.Err()
		},
		Timeout: 30 * time.Second,
	})
	if err != nil {
		_ = backend.Close()
		return nil, fmt.Errorf("open provider session: %w", err)
	}
	return session, nil
}

// withProviderClient opens a provider session, creates an inference client,
// and calls fn. The session is closed when fn returns.
func withProviderClient(ctx context.Context, providerID string, fn func(*ginference.Client, *plugin.Session) error) error {
	cfg, err := resolveProvider(providerID)
	if err != nil {
		return err
	}
	session, err := openProviderSession(ctx, cfg)
	if err != nil {
		return err
	}
	defer session.Close(ctx)
	client := ginference.NewClient(session)
	return fn(client, session)
}

// inferenceProvidersCommand lists available inference providers.
func inferenceProvidersCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "ctn: inference providers takes no arguments")
		return 2
	}
	// List providers from environment variables.
	for _, env := range os.Environ() {
		key, value, ok := strings.Cut(env, "=")
		if !ok || !strings.HasPrefix(key, "EXT_INFERENCE_PROVIDER_") {
			continue
		}
		id := strings.ToLower(strings.TrimPrefix(key, "EXT_INFERENCE_PROVIDER_"))
		id = strings.ReplaceAll(id, "_", "-")
		fmt.Fprintf(stdout, "%s\t%s\n", id, value)
	}
	return 0
}

// inferenceCapabilitiesCommand shows provider capabilities.
func inferenceCapabilitiesCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ctn inference capabilities", flag.ContinueOnError)
	flags.SetOutput(stderr)
	provider := flags.String("provider", "", "provider ID (required)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *provider == "" {
		fmt.Fprintln(stderr, "ctn: --provider is required")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return checkError(withProviderClient(ctx, *provider, func(client *ginference.Client, _ *plugin.Session) error {
		info, err := client.Capabilities(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "ctn: %v\n", err)
			return err
		}
		data, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "ctn: %v\n", err)
			return err
		}
		fmt.Fprintf(stdout, "%s\n", string(data))
		return nil
	}))
}

// inferenceHealthCommand checks provider health.
func inferenceHealthCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ctn inference health", flag.ContinueOnError)
	flags.SetOutput(stderr)
	provider := flags.String("provider", "", "provider ID (required)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *provider == "" {
		fmt.Fprintln(stderr, "ctn: --provider is required")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return checkError(withProviderClient(ctx, *provider, func(client *ginference.Client, _ *plugin.Session) error {
		h, err := client.Health(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "ctn: %v\n", err)
			return err
		}
		fmt.Fprintf(stdout, "status=%s ready=%v\n", h.Status, h.Ready)
		return nil
	}))
}

// inferenceGenerateCommand runs one generation pass.
func inferenceGenerateCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ctn inference generate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	provider := flags.String("provider", "", "provider ID (required)")
	model := flags.String("model", "", "model ID (required)")
	maxTokens := flags.Int("max-tokens", 0, "maximum output tokens")
	input := flags.String("input", "", "input file (JSON GenerationRequest)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *provider == "" {
		fmt.Fprintln(stderr, "ctn: --provider is required")
		return 2
	}
	if *model == "" {
		fmt.Fprintln(stderr, "ctn: --model is required")
		return 2
	}

	var req inference.GenerationRequest
	req.Model = *model
	req.MaxOutputTokens = *maxTokens
	if *input != "" {
		data, err := os.ReadFile(*input)
		if err != nil {
			fmt.Fprintf(stderr, "ctn: cannot read input: %v\n", err)
			return 1
		}
		if err := json.Unmarshal(data, &req); err != nil {
			fmt.Fprintf(stderr, "ctn: invalid input: %v\n", err)
			return 1
		}
		req.Model = *model
		req.MaxOutputTokens = *maxTokens
	} else {
		fmt.Fprintln(stderr, "ctn: --input is required (JSON GenerationRequest)")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	return checkError(withProviderClient(ctx, *provider, func(client *ginference.Client, _ *plugin.Session) error {
		resp, err := client.Generate(ctx, req)
		if err != nil {
			fmt.Fprintf(stderr, "ctn: %v\n", err)
			return err
		}
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "ctn: %v\n", err)
			return err
		}
		fmt.Fprintf(stdout, "%s\n", string(data))
		return nil
	}))
}

// inferenceStreamCommand streams generation events.
func inferenceStreamCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ctn inference stream", flag.ContinueOnError)
	flags.SetOutput(stderr)
	provider := flags.String("provider", "", "provider ID (required)")
	model := flags.String("model", "", "model ID (required)")
	input := flags.String("input", "", "input file (JSON GenerationRequest)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *provider == "" || *model == "" || *input == "" {
		fmt.Fprintln(stderr, "ctn: inference stream needs --provider, --model, and --input")
		return 2
	}

	var req inference.GenerationRequest
	data, err := os.ReadFile(*input)
	if err != nil {
		fmt.Fprintf(stderr, "ctn: cannot read input: %v\n", err)
		return 1
	}
	if err := json.Unmarshal(data, &req); err != nil {
		fmt.Fprintf(stderr, "ctn: invalid input: %v\n", err)
		return 1
	}
	req.Model = *model

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	return checkError(withProviderClient(ctx, *provider, func(client *ginference.Client, _ *plugin.Session) error {
		stream, err := client.Stream(ctx, req)
		if err != nil {
			fmt.Fprintf(stderr, "ctn: %v\n", err)
			return err
		}
		defer stream.Close(ctx)
		for {
			batch, err := stream.Read(ctx, 16)
			if err != nil {
				if err == io.EOF {
					break
				}
				fmt.Fprintf(stderr, "ctn: %v\n", err)
				return err
			}
			for _, item := range batch.Items {
				var ev inference.GenerationEvent
				if err := json.Unmarshal(item, &ev); err != nil {
					continue
				}
				if ev.Text != "" {
					fmt.Fprint(stdout, ev.Text)
				}
			}
			if batch.Done {
				break
			}
		}
		fmt.Fprintln(stdout)
		return nil
	}))
}

// inferenceEmbedCommand returns embeddings.
func inferenceEmbedCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ctn inference embed", flag.ContinueOnError)
	flags.SetOutput(stderr)
	provider := flags.String("provider", "", "provider ID (required)")
	model := flags.String("model", "", "model ID (required)")
	input := flags.String("input", "", "input file (JSON EmbeddingRequest)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *provider == "" || *model == "" || *input == "" {
		fmt.Fprintln(stderr, "ctn: inference embed needs --provider, --model, and --input")
		return 2
	}

	var req inference.EmbeddingRequest
	data, err := os.ReadFile(*input)
	if err != nil {
		fmt.Fprintf(stderr, "ctn: cannot read input: %v\n", err)
		return 1
	}
	if err := json.Unmarshal(data, &req); err != nil {
		fmt.Fprintf(stderr, "ctn: invalid input: %v\n", err)
		return 1
	}
	req.Model = *model

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	return checkError(withProviderClient(ctx, *provider, func(client *ginference.Client, _ *plugin.Session) error {
		resp, err := client.Embed(ctx, req)
		if err != nil {
			fmt.Fprintf(stderr, "ctn: %v\n", err)
			return err
		}
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "ctn: %v\n", err)
			return err
		}
		fmt.Fprintf(stdout, "%s\n", string(data))
		return nil
	}))
}

// inferenceTranscribeCommand transcribes audio.
func inferenceTranscribeCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ctn inference transcribe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	provider := flags.String("provider", "", "provider ID (required)")
	model := flags.String("model", "", "model ID (required)")
	input := flags.String("input", "", "input file (JSON TranscriptionRequest)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *provider == "" || *model == "" || *input == "" {
		fmt.Fprintln(stderr, "ctn: inference transcribe needs --provider, --model, and --input")
		return 2
	}

	var req inference.TranscriptionRequest
	data, err := os.ReadFile(*input)
	if err != nil {
		fmt.Fprintf(stderr, "ctn: cannot read input: %v\n", err)
		return 1
	}
	if err := json.Unmarshal(data, &req); err != nil {
		fmt.Fprintf(stderr, "ctn: invalid input: %v\n", err)
		return 1
	}
	req.Model = *model

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	return checkError(withProviderClient(ctx, *provider, func(client *ginference.Client, _ *plugin.Session) error {
		resp, err := client.Transcribe(ctx, req)
		if err != nil {
			fmt.Fprintf(stderr, "ctn: %v\n", err)
			return err
		}
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "ctn: %v\n", err)
			return err
		}
		fmt.Fprintf(stdout, "%s\n", string(data))
		return nil
	}))
}

// inferenceSynthesizeCommand synthesizes speech.
func inferenceSynthesizeCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ctn inference synthesize", flag.ContinueOnError)
	flags.SetOutput(stderr)
	provider := flags.String("provider", "", "provider ID (required)")
	model := flags.String("model", "", "model ID (required)")
	input := flags.String("input", "", "input file (JSON SpeechRequest)")
	output := flags.String("output", "", "output audio file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *provider == "" || *model == "" || *input == "" {
		fmt.Fprintln(stderr, "ctn: inference synthesize needs --provider, --model, and --input")
		return 2
	}

	var req inference.SpeechRequest
	data, err := os.ReadFile(*input)
	if err != nil {
		fmt.Fprintf(stderr, "ctn: cannot read input: %v\n", err)
		return 1
	}
	if err := json.Unmarshal(data, &req); err != nil {
		fmt.Fprintf(stderr, "ctn: invalid input: %v\n", err)
		return 1
	}
	req.Model = *model

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	return checkError(withProviderClient(ctx, *provider, func(client *ginference.Client, _ *plugin.Session) error {
		stream, err := client.Synthesize(ctx, req)
		if err != nil {
			fmt.Fprintf(stderr, "ctn: %v\n", err)
			return err
		}
		defer stream.Close(ctx)
		var out io.Writer = stdout
		if *output != "" {
			f, err := os.Create(*output)
			if err != nil {
				fmt.Fprintf(stderr, "ctn: cannot create output: %v\n", err)
				return err
			}
			defer f.Close()
			out = f
		}
		buf := make([]byte, 64<<10)
		for {
			batch, err := stream.Read(ctx, len(buf))
			if err != nil {
				if err == io.EOF {
					break
				}
				fmt.Fprintf(stderr, "ctn: %v\n", err)
				return err
			}
			for _, item := range batch.Items {
				if _, err := out.Write(item); err != nil {
					fmt.Fprintf(stderr, "ctn: write failed: %v\n", err)
					return err
				}
			}
			if batch.Done {
				break
			}
		}
		return nil
	}))
}

// inferenceModelsCommand lists provider models.
func inferenceModelsCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ctn inference models", flag.ContinueOnError)
	flags.SetOutput(stderr)
	provider := flags.String("provider", "", "provider ID (required)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *provider == "" {
		fmt.Fprintln(stderr, "ctn: --provider is required")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return checkError(withProviderClient(ctx, *provider, func(client *ginference.Client, _ *plugin.Session) error {
		models, err := client.Models(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "ctn: %v\n", err)
			return err
		}
		for _, m := range models {
			fmt.Fprintf(stdout, "%s\n", m.ID)
		}
		return nil
	}))
}
