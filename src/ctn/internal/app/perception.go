package app

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/cgoengine"
	"github.com/webong/ext/pkg/ext-go/engine/host/home"
	"github.com/webong/ext/pkg/ext-go/engine/host/process"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	hosttrust "github.com/webong/ext/pkg/ext-go/engine/host/trust"
	bridge "github.com/webong/ext/pkg/ext-go/perception"
	domain "github.com/webong/ext/res/int/perception"
)

func perceptionCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctn: perception needs package, install, trust, untrust, providers, capabilities, observe, or interpret")
		return 2
	}
	switch args[0] {
	case "package":
		flags := flag.NewFlagSet("ctn perception package", flag.ContinueOnError)
		flags.SetOutput(stderr)
		goos := flags.String("os", "", "target operating system")
		goarch := flags.String("arch", "", "target architecture")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		values := flags.Args()
		if len(values) != 4 {
			fmt.Fprintln(stderr, "ctn: perception package needs ID VERSION BINARY OUTPUT_DIR")
			return 2
		}
		manifest, err := bridge.PackageExecutable(bridge.ExecutablePackage{ID: values[0], Version: values[1], Binary: values[2], OutputDir: values[3], OS: *goos, Arch: *goarch})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\t%s\n", manifest.Name(), values[3])
		return 0
	case "install":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "ctn: perception install needs PACKAGE_DIR")
			return 2
		}
		s, err := store.New(home.Intelligence(), store.Limits{})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		installed, err := s.Install(args[1])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !installed.Supports(bridge.ContractName, bridge.ContractVersion) {
			_ = s.Remove(installed.Name())
			fmt.Fprintln(stderr, "ctn: package does not declare intelligence.perception/v1")
			return 1
		}
		digest, err := installed.Digest()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\t%s\n", installed.Name(), digest)
		return 0
	case "trust", "untrust":
		if len(args) != 2 {
			fmt.Fprintf(stderr, "ctn: perception %s needs ID\n", args[0])
			return 2
		}
		file, err := inferenceTrustFile()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if args[0] == "untrust" {
			if err := file.Revoke(bridge.Protocol, args[1]); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			fmt.Fprintln(stdout, args[1])
			return 0
		}
		s, err := store.New(home.Intelligence(), store.Limits{})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		installed, err := s.Load(args[1])
		if err != nil || !installed.Supports(bridge.ContractName, bridge.ContractVersion) {
			fmt.Fprintln(stderr, "ctn: installed perception provider is required")
			return 1
		}
		digest, err := installed.Digest()
		if err == nil {
			err = os.MkdirAll(filepath.Dir(file.Path), 0o700)
		}
		if err == nil {
			err = file.Record(bridge.Protocol, args[1], digest)
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\t%s\n", args[1], digest)
		return 0
	case "providers":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "ctn: perception providers takes no arguments")
			return 2
		}
		s, err := store.New(home.Intelligence(), store.Limits{})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		installed, err := s.List()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		for _, item := range installed {
			if !item.Supports(bridge.ContractName, bridge.ContractVersion) {
				continue
			}
			digest, err := item.Digest()
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			state := "untrusted"
			if _, err := trustedPerceptionDigest(item.Name(), s); err == nil {
				state = "trusted"
			}
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", item.Name(), state, digest)
		}
		return 0
	case "capabilities", "observe", "interpret":
		return perceptionCall(args, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "ctn: unknown perception action %s\n", args[0])
		return 2
	}
}

func trustedPerceptionDigest(id string, s *store.Store) (string, error) {
	installed, err := s.Load(id)
	if err != nil {
		return "", err
	}
	if !installed.Supports(bridge.ContractName, bridge.ContractVersion) {
		return "", fmt.Errorf("%w: package does not declare %s", plugin.ErrUnsupported, bridge.Protocol)
	}
	digest, err := installed.Digest()
	if err != nil {
		return "", err
	}
	file, err := inferenceTrustFile()
	if err != nil {
		return "", err
	}
	state, err := file.StateOf(bridge.Protocol, id, digest)
	if err != nil {
		return "", err
	}
	if state != hosttrust.Trusted {
		return "", fmt.Errorf("%w: perception provider %s requires trust", plugin.ErrDenied, id)
	}
	return digest, nil
}

func withPerceptionClient(ctx context.Context, id string, fn func(*bridge.Client) error) error {
	if id == "" {
		return fmt.Errorf("%w: provider ID is required", plugin.ErrInvalid)
	}
	s, err := store.New(home.Intelligence(), store.Limits{})
	if err != nil {
		return err
	}
	digest, err := trustedPerceptionDigest(id, s)
	if err != nil {
		return err
	}
	selected, err := s.ResolveEntrypoint(id, bridge.ContractName, bridge.ContractVersion, bridge.Protocol, digest)
	if err != nil {
		return err
	}
	executable, err := executableDigest(selected.Path)
	if err != nil {
		return err
	}
	descriptor := bridge.Descriptor(plugin.Identity{ID: id, Revision: executable, Version: selected.Package.Version()})
	session, err := plugin.Open(ctx, descriptor, plugin.Options{
		Verify: func(_ context.Context, d plugin.Descriptor) error {
			fresh, err := store.New(home.Intelligence(), store.Limits{})
			if err != nil {
				return err
			}
			want, err := trustedPerceptionDigest(id, fresh)
			if err != nil || want != digest {
				return plugin.ErrDenied
			}
			current, err := fresh.ResolveEntrypoint(id, bridge.ContractName, bridge.ContractVersion, bridge.Protocol, digest)
			if err != nil || current.Artifact != selected.Artifact || filepath.Clean(current.Path) != filepath.Clean(selected.Path) {
				return plugin.ErrDenied
			}
			actual, err := executableDigest(current.Path)
			if err != nil || actual != d.Identity.Revision {
				return plugin.ErrDenied
			}
			return nil
		},
		Connect: func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
			return process.Start(ctx, process.Command{Path: selected.Path, Args: []string{"run"}, Env: os.Environ()})
		},
		Authorize: func(ctx context.Context, _ plugin.Request) error { return ctx.Err() },
		Timeout:   30 * time.Second,
	})
	if err != nil {
		return err
	}
	defer session.Close(ctx)
	return fn(bridge.NewClient(session))
}

func perceptionCall(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ctn perception "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	provider := flags.String("provider", "", "installed provider ID")
	model := flags.String("model", "", "selected model ID")
	input := flags.String("input", "", "request JSON file")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if *provider == "" || len(flags.Args()) != 0 || (args[0] != "capabilities" && (*model == "" || *input == "")) {
		fmt.Fprintln(stderr, "ctn: --provider is required; observe and interpret also need --model and --input")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	err := withPerceptionClient(ctx, *provider, func(client *bridge.Client) error {
		var value any
		var err error
		switch args[0] {
		case "capabilities":
			value, err = client.Info(ctx)
		case "observe":
			var request domain.ObserveRequest
			if err = readPerceptionRequest(*input, &request); err == nil {
				request.APIVersion, request.Model = domain.APIVersion, *model
				value, err = client.Observe(ctx, request)
			}
		case "interpret":
			var request domain.InterpretRequest
			if err = readPerceptionRequest(*input, &request); err == nil {
				request.APIVersion, request.Model = domain.APIVersion, *model
				value, err = client.Interpret(ctx, request)
			}
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(value)
	})
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	return 0
}

func readPerceptionRequest(path string, result any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	const maxRequestBytes = 16 << 20
	data, err := io.ReadAll(io.LimitReader(file, maxRequestBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxRequestBytes {
		return fmt.Errorf("%w: request exceeds %d bytes", plugin.ErrInvalid, maxRequestBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%w: request must contain exactly one JSON object", plugin.ErrInvalid)
	}
	return nil
}
