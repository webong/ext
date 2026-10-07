package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	browsershare "github.com/webong/ext/res/browser/contract"
	"github.com/webong/ext/src/ctx/internal/config"
)

type browserPolicyBundle = browsershare.PolicyBundle

func shareBrowserPolicyCommand(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "export" {
		fmt.Fprintln(stderr, "ctx: share:browser policy requires export")
		return 2
	}
	flags := flag.NewFlagSet("share:browser policy export", flag.ContinueOnError)
	flags.SetOutput(stderr)
	from := flags.String("from", "", "source browser:profile")
	toFile := flags.String("to-file", "", "new file for the policy bundle")
	toStdout := flags.Bool("stdout", false, "write the policy bundle to a pipe")
	if err := flags.Parse(args[1:]); err != nil || len(flags.Args()) != 0 || (*toFile == "") == !*toStdout {
		fmt.Fprintln(stderr, "ctx: policy export needs exactly one of --to-file or --stdout")
		return 2
	}
	source, err := resolveBrowserSource(resolver, *from)
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	if code := invokeAdapter(resolver, source.Adapter, "validate", source.Profile, nil, "", io.Discard, stderr); code != 0 {
		return reportError(stderr, fmt.Errorf("browser profile %s:%s is unavailable", source.Adapter.Manifest.Name, source.Profile))
	}
	if *toStdout {
		if file, ok := stdout.(*os.File); ok {
			info, err := file.Stat()
			if err != nil {
				return reportError(stderr, err)
			}
			if info.Mode()&os.ModeNamedPipe == 0 {
				fmt.Fprintln(stderr, "ctx: --stdout requires a pipe; use --to-file for a protected file")
				return 2
			}
		}
	} else if _, err := os.Lstat(*toFile); err == nil {
		fmt.Fprintln(stderr, "ctx: output file already exists")
		return 1
	} else if !errors.Is(err, os.ErrNotExist) {
		return reportError(stderr, err)
	}
	var bundle browserPolicyBundle
	if err := browserAdapterShare(resolver, source, "policy", "export", browserShareRequest{}, &bundle, stderr); err != nil {
		return reportError(stderr, err)
	}
	if bundle.Version != browsershare.Version {
		return reportError(stderr, errors.New("adapter returned an unsupported policy bundle version"))
	}
	bundle.Source = source.Adapter.Manifest.Name + ":" + source.Profile
	if *toStdout {
		if err := json.NewEncoder(stdout).Encode(bundle); err != nil {
			return reportError(stderr, err)
		}
		return 0
	}
	if err := writePrivateJSON(*toFile, bundle); err != nil {
		return reportError(stderr, err)
	}
	fmt.Fprintf(stdout, "exported %d policy sources into %s (mode 0600)\n", len(bundle.Entries), *toFile)
	return 0
}

func writePrivateJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writePrivateOutputSize(path, uint64(len(data)), func(output io.Writer) error {
		_, err := output.Write(data)
		return err
	})
}

func writePrivateOutput(path string, write func(io.Writer) error) error {
	return writePrivateOutputSize(path, 1, write)
}

func writePrivateOutputSize(path string, minimumBytes uint64, write func(io.Writer) error) error {
	if err := prepareOutputDirectory("file.export", filepath.Dir(path), minimumBytes); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := write(file); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}
