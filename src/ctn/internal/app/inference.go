// Package app implements the ctn command. This file adds inference-resource
// commands that discover and invoke installed inference providers through the
// Ext plugin engine.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/webong/ext/pkg/ext-go/engine/host/home"
	"github.com/webong/ext/pkg/ext-go/engine/host/process"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	hosttrust "github.com/webong/ext/pkg/ext-go/engine/host/trust"
	ginference "github.com/webong/ext/pkg/ext-go/inference"
	inference "github.com/webong/ext/res/inference"
)

// inferenceCommand lists providers and runs inference operations through an
// explicitly selected provider plugin.
func inferenceCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctn: inference needs one of package, install, trust, untrust, providers, capabilities, health, generate, stream, embed, transcribe, synthesize, or models")
		return 2
	}
	switch args[0] {
	case "package":
		return inferencePackageCommand(args[1:], stdout, stderr)
	case "install":
		return inferenceInstallCommand(args[1:], stdout, stderr)
	case "trust":
		return inferenceTrustCommand(args[1:], stdout, stderr)
	case "untrust":
		return inferenceUntrustCommand(args[1:], stdout, stderr)
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

func inferencePackageCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ctn inference package", flag.ContinueOnError)
	flags.SetOutput(stderr)
	goos := flags.String("os", "", "target operating system (default: current OS)")
	goarch := flags.String("arch", "", "target architecture (default: current architecture)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	values := flags.Args()
	if len(values) != 4 {
		fmt.Fprintln(stderr, "ctn: inference package requires ID VERSION BINARY OUTPUT_DIR")
		return 2
	}
	manifest, err := ginference.PackageExecutable(ginference.ExecutablePackage{ID: values[0], Version: values[1], Binary: values[2], OutputDir: values[3], OS: *goos, Arch: *goarch})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "%s\t%s\n", manifest.Name(), values[3])
	return 0
}

func inferenceInstallCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "ctn: inference install requires one sealed package directory")
		return 2
	}
	s, err := store.New(inferencePackageRoot(), store.Limits{})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	installed, err := s.Install(args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !installed.Supports(ginference.ContractName, ginference.ContractVersion) {
		_ = s.Remove(installed.Name())
		fmt.Fprintln(stderr, "ctn: package does not declare inference.provider/v1")
		return 1
	}
	digest, err := installed.Digest()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "%s\t%s\n", installed.Name(), digest)
	return 0
}

// providerConfig describes how to connect to an inference provider.
type providerConfig struct {
	ID       string
	Root     string
	Path     string
	Artifact string
	Digest   string
	Version  string
}

func inferencePackageRoot() string {
	return home.Inference()
}

const inferenceTrustScope = "inference.provider/v1"

func inferenceTrustFile() (*hosttrust.File, error) {
	return hosttrust.New(filepath.Join(home.Config(), "package-trust.json"))
}

func inferenceTrustCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "ctn: inference trust requires one installed provider ID")
		return 2
	}
	s, err := store.New(inferencePackageRoot(), store.Limits{})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	installed, err := s.Load(args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !installed.Supports(ginference.ContractName, ginference.ContractVersion) {
		fmt.Fprintln(stderr, "ctn: package does not declare inference.provider/v1")
		return 1
	}
	digest, err := installed.Digest()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	file, err := inferenceTrustFile()
	if err == nil {
		err = os.MkdirAll(filepath.Dir(file.Path), 0o700)
	}
	if err == nil {
		err = file.Record(inferenceTrustScope, args[0], digest)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "%s\t%s\n", args[0], digest)
	return 0
}

func inferenceUntrustCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "ctn: inference untrust requires one provider ID")
		return 2
	}
	file, err := inferenceTrustFile()
	if err == nil {
		err = file.Revoke(inferenceTrustScope, args[0])
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, args[0])
	return 0
}

func trustedProviderDigest(id string, s *store.Store) (string, error) {
	installed, err := s.Load(id)
	if err != nil {
		return "", err
	}
	digest, err := installed.Digest()
	if err != nil {
		return "", err
	}
	file, err := inferenceTrustFile()
	if err != nil {
		return "", err
	}
	state, err := file.StateOf(inferenceTrustScope, id, digest)
	if err != nil {
		return "", err
	}
	if state == hosttrust.Trusted {
		return digest, nil
	}
	// Existing explicit grants remain accepted while callers migrate to the
	// persistent scoped ledger.
	trustKey := "EXT_INFERENCE_TRUST_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_", "/", "_").Replace(id))
	if os.Getenv(trustKey) == digest {
		return digest, nil
	}
	return "", fmt.Errorf("%w: package %s requires explicit trust", inference.ErrAuthorization, id)
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
// installed and explicitly trusted inference-provider package executable.
func resolveProvider(id string) (providerConfig, error) {
	if id == "" {
		return providerConfig{}, fmt.Errorf("%w: provider ID is required", inference.ErrInvalidRequest)
	}
	root := inferencePackageRoot()
	s, err := store.New(root, store.Limits{})
	if err != nil {
		return providerConfig{}, err
	}
	want, err := trustedProviderDigest(id, s)
	if err != nil {
		return providerConfig{}, err
	}
	selected, err := s.ResolveEntrypoint(id, ginference.ContractName, ginference.ContractVersion, "inference.provider/v1", want)
	if err != nil {
		return providerConfig{}, err
	}
	return providerConfig{ID: id, Root: root, Path: selected.Path, Artifact: selected.Artifact, Digest: selected.Digest, Version: selected.Package.Version()}, nil
}

// openProviderSession starts the provider plugin executable and opens a plugin
// session to it. The caller must close the returned session.
func openProviderSession(ctx context.Context, cfg providerConfig) (*ginference.Session, error) {
	digest, err := executableDigest(cfg.Path)
	if err != nil {
		return nil, err
	}
	// The provider serves inference.provider/v1. Open a session to it.
	descriptor := ginference.Descriptor(ginference.Identity{ID: cfg.ID, Revision: digest, Version: cfg.Version})
	session, err := ginference.Open(ctx, descriptor, ginference.Options{
		Verify: func(ctx context.Context, d ginference.DescriptorType) error {
			s, err := store.New(cfg.Root, store.Limits{})
			if err != nil {
				return err
			}
			want, err := trustedProviderDigest(cfg.ID, s)
			if err != nil || want != cfg.Digest {
				return inference.ErrAuthorization
			}
			selected, err := s.ResolveEntrypoint(cfg.ID, ginference.ContractName, ginference.ContractVersion, "inference.provider/v1", cfg.Digest)
			if err != nil {
				return err
			}
			if selected.Artifact != cfg.Artifact || filepath.Clean(selected.Path) != filepath.Clean(cfg.Path) {
				return inference.ErrAuthorization
			}
			actual, err := executableDigest(selected.Path)
			if err != nil {
				return err
			}
			if actual != d.Identity.Revision {
				return inference.ErrAuthorization
			}
			return nil
		},
		Connect: func(ctx context.Context, d ginference.DescriptorType) (ginference.Backend, error) {
			return process.Start(ctx, process.Command{Path: cfg.Path, Args: []string{"run"}, Env: os.Environ()})
		},
		Authorize: func(ctx context.Context, req ginference.Request) error {
			return ctx.Err()
		},
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("open provider session: %w", err)
	}
	return session, nil
}

func executableDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// withProviderClient opens a provider session, creates an inference client,
// and calls fn. The session is closed when fn returns.
func withProviderClient(ctx context.Context, providerID string, fn func(*ginference.Client, *ginference.Session) error) error {
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
	root := inferencePackageRoot()
	s, err := store.New(root, store.Limits{})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	installed, err := s.List()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	for _, p := range installed {
		if !p.Supports(ginference.ContractName, ginference.ContractVersion) {
			continue
		}
		digest, err := p.Digest()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		state := "untrusted"
		if _, err := resolveProvider(p.Name()); err == nil {
			state = "trusted"
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", p.Name(), state, digest)
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
	return checkError(withProviderClient(ctx, *provider, func(client *ginference.Client, _ *ginference.Session) error {
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
	return checkError(withProviderClient(ctx, *provider, func(client *ginference.Client, _ *ginference.Session) error {
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
	return checkError(withProviderClient(ctx, *provider, func(client *ginference.Client, _ *ginference.Session) error {
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
	return checkError(withProviderClient(ctx, *provider, func(client *ginference.Client, _ *ginference.Session) error {
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
	return checkError(withProviderClient(ctx, *provider, func(client *ginference.Client, _ *ginference.Session) error {
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
	return checkError(withProviderClient(ctx, *provider, func(client *ginference.Client, _ *ginference.Session) error {
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
	return checkError(withProviderClient(ctx, *provider, func(client *ginference.Client, _ *ginference.Session) error {
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
	return checkError(withProviderClient(ctx, *provider, func(client *ginference.Client, _ *ginference.Session) error {
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
