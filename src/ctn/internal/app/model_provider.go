package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	plugin "github.com/webong/ext/pkg/ext-go/cgoengine"
	"github.com/webong/ext/pkg/ext-go/engine/host/home"
	"github.com/webong/ext/pkg/ext-go/engine/host/process"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	hosttrust "github.com/webong/ext/pkg/ext-go/engine/host/trust"
	mlplugin "github.com/webong/ext/pkg/ext-go/ml"
	"github.com/webong/ext/res/ml"
)

const modelTrustScope = mlplugin.Protocol

func modelProviderCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctn: model provider needs package, install, list, trust, or untrust")
		return 2
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "ctn: model provider list takes no arguments")
			return 2
		}
		s, err := store.New(home.Inference(), store.Limits{})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		installed, err := s.List()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		file, err := modelTrustFile()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		for _, item := range installed {
			if !item.Supports(mlplugin.ContractName, mlplugin.ContractVersion) {
				continue
			}
			digest, err := item.Digest()
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			state, err := file.StateOf(modelTrustScope, item.Name(), digest)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			status := "untrusted"
			switch state {
			case hosttrust.Trusted:
				status = "trusted"
			case hosttrust.Changed:
				status = "changed"
			}
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", item.Name(), item.Version(), status, digest)
		}
		return 0
	case "package":
		flags := flag.NewFlagSet("ctn model provider package", flag.ContinueOnError)
		flags.SetOutput(stderr)
		goos := flags.String("os", "", "target operating system")
		goarch := flags.String("arch", "", "target architecture")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		values := flags.Args()
		if len(values) != 4 {
			fmt.Fprintln(stderr, "ctn: model provider package needs ID VERSION BINARY OUTPUT_DIR")
			return 2
		}
		manifest, err := mlplugin.PackageExecutable(mlplugin.ExecutablePackage{
			ID: values[0], Version: values[1], Binary: values[2], OutputDir: values[3], OS: *goos, Arch: *goarch,
		})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\t%s\n", manifest.Name(), values[3])
		return 0
	case "install":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "ctn: model provider install needs one sealed package directory")
			return 2
		}
		s, err := store.New(home.Inference(), store.Limits{})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		installed, err := s.Install(args[1])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !installed.Supports(mlplugin.ContractName, mlplugin.ContractVersion) {
			_ = s.Remove(installed.Name())
			fmt.Fprintln(stderr, "ctn: package does not declare model.tensor/v1")
			return 1
		}
		digest, err := installed.Digest()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\t%s\n", installed.Name(), digest)
		return 0
	case "trust":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "ctn: model provider trust needs one installed package ID")
			return 2
		}
		s, err := store.New(home.Inference(), store.Limits{})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		installed, err := s.Load(args[1])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !installed.Supports(mlplugin.ContractName, mlplugin.ContractVersion) {
			fmt.Fprintln(stderr, "ctn: package does not declare model.tensor/v1")
			return 1
		}
		digest, err := installed.Digest()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		file, err := modelTrustFile()
		if err == nil {
			err = os.MkdirAll(filepath.Dir(file.Path), 0o700)
		}
		if err == nil {
			err = file.Record(modelTrustScope, args[1], digest)
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\t%s\n", args[1], digest)
		return 0
	case "untrust":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "ctn: model provider untrust needs one package ID")
			return 2
		}
		file, err := modelTrustFile()
		if err == nil {
			err = file.Revoke(modelTrustScope, args[1])
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, args[1])
		return 0
	default:
		fmt.Fprintf(stderr, "ctn: unknown model provider action %s\n", args[0])
		return 2
	}
}

func modelTrustFile() (*hosttrust.File, error) {
	return hosttrust.New(filepath.Join(home.Config(), "package-trust.json"))
}

func resolveModelProvider(id string) (store.Entrypoint, string, error) {
	s, err := store.New(home.Inference(), store.Limits{})
	if err != nil {
		return store.Entrypoint{}, "", err
	}
	installed, err := s.Load(id)
	if err != nil {
		return store.Entrypoint{}, "", err
	}
	digest, err := installed.Digest()
	if err != nil {
		return store.Entrypoint{}, "", err
	}
	file, err := modelTrustFile()
	if err != nil {
		return store.Entrypoint{}, "", err
	}
	state, err := file.StateOf(modelTrustScope, id, digest)
	if err != nil {
		return store.Entrypoint{}, "", err
	}
	if state != hosttrust.Trusted {
		return store.Entrypoint{}, "", fmt.Errorf("%w: model provider %s is not trusted or changed", plugin.ErrDenied, id)
	}
	selected, err := s.ResolveEntrypoint(id, mlplugin.ContractName, mlplugin.ContractVersion, mlplugin.Protocol, digest)
	return selected, digest, err
}

func runTensorProvider(ctx context.Context, id, root string, ref ml.Reference, inputs []ml.Tensor) ([]ml.Tensor, error) {
	selected, digest, err := resolveModelProvider(id)
	if err != nil {
		return nil, err
	}
	descriptor := selected.Package.Manifest.Descriptor
	session, err := plugin.Open(ctx, descriptor, plugin.Options{
		Verify: func(ctx context.Context, d plugin.Descriptor) error {
			current, currentDigest, err := resolveModelProvider(id)
			if err != nil || currentDigest != digest || current.Artifact != selected.Artifact || filepath.Clean(current.Path) != filepath.Clean(selected.Path) {
				return plugin.ErrDenied
			}
			f, err := os.Open(current.Path)
			if err != nil {
				return err
			}
			defer f.Close()
			h := sha256.New()
			if _, err := io.Copy(h, f); err != nil {
				return err
			}
			if d.Identity.Revision != hex.EncodeToString(h.Sum(nil)) {
				return plugin.ErrDenied
			}
			return nil
		},
		Connect: func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
			return process.Start(ctx, process.Command{Path: selected.Path, Args: []string{"run"}, Env: os.Environ()})
		},
		Authorize: func(ctx context.Context, request plugin.Request) error {
			if request.Contract != mlplugin.ContractRef || request.Operation != "infer" {
				return plugin.ErrDenied
			}
			return ctx.Err()
		},
	})
	if err != nil {
		return nil, err
	}
	defer session.Close(ctx)
	response, err := mlplugin.NewClient(session).Infer(ctx, mlplugin.InferRequest{Root: root, Reference: ref.String(), Inputs: inputs})
	if err != nil {
		var remote *plugin.RemoteError
		if errors.As(err, &remote) {
			return nil, fmt.Errorf("model provider: %s", remote.Message)
		}
		return nil, err
	}
	return response.Outputs, nil
}
