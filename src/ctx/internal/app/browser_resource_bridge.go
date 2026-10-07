package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/webong/ext/ctx/internal/config"
	browsershare "github.com/webong/ext/res/web/contract"
)

var browserResourceName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// A resource payload is owned by its adapters. ctx validates the envelope and
// routes it without interpreting browser-specific secrets or policy formats.
type browserResourceBundle = browsershare.ResourceBundle
type browserResourceRequest = browsershare.ResourceRequest

func shareBrowserGenericCommand(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || !browserResourceName.MatchString(args[0]) {
		fmt.Fprintln(stderr, "ctx: browser resource needs <resource> <list|export|copy|import>")
		return 2
	}
	resource, action := args[0], args[1]
	if action != "list" && action != "export" && action != "copy" && action != "import" {
		fmt.Fprintln(stderr, "ctx: browser resource needs list, export, copy, or import")
		return 2
	}
	flags := flag.NewFlagSet("share:browser "+resource+" "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	from := flags.String("from", "", "source browser:profile")
	fromFile := flags.String("from-file", "", "resource bundle file")
	fromStdin := flags.Bool("stdin", false, "read a resource bundle from a pipe")
	toProfile := flags.String("to-profile", "", "destination browser:profile")
	toFile := flags.String("to-file", "", "new resource bundle file")
	toStdout := flags.Bool("stdout", false, "write a resource bundle to a pipe")
	replace := flags.Bool("replace", false, "replace an existing target resource")
	if err := flags.Parse(args[2:]); err != nil {
		return 2
	}
	adapterArgs := flags.Args()
	if action == "import" {
		if *from != "" || (*fromFile == "") == !*fromStdin || *toProfile == "" || *toFile != "" || *toStdout {
			fmt.Fprintln(stderr, "ctx: resource import needs --from-file or --stdin and --to-profile")
			return 2
		}
		return importBrowserResource(resolver, resource, *fromFile, *toProfile, *replace, adapterArgs, stdout, stderr)
	}
	if *fromFile != "" || *fromStdin || (action != "copy" && *replace) {
		fmt.Fprintln(stderr, "ctx: invalid resource source or replacement flags")
		return 2
	}
	source, err := resolveBrowserSource(resolver, *from)
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	request := browserResourceRequest{Version: browsershare.Version, Args: adapterArgs}
	if action == "list" {
		if *toProfile != "" || *toFile != "" || *toStdout {
			return reportErrorCode(stderr, errors.New("resource list accepts no destination"), 2)
		}
		var result json.RawMessage
		if err := invokeBrowserShareJSON(resolver, source, resource, "list", request, &result, stderr); err != nil {
			return reportError(stderr, err)
		}
		_, err := fmt.Fprintln(stdout, string(result))
		if err != nil {
			return reportError(stderr, err)
		}
		return 0
	}
	if action == "copy" {
		if *toProfile == "" || *toFile != "" || *toStdout {
			return reportErrorCode(stderr, errors.New("resource copy needs --to-profile"), 2)
		}
		target, err := parseBrowserEndpoint(*toProfile)
		if err != nil {
			return reportErrorCode(stderr, err, 2)
		}
		if !target.Adapter.HasBrowserShare(resource + ".import") {
			return reportError(stderr, fmt.Errorf("%s adapter cannot import %s", target.Adapter.Manifest.Name, resource))
		}
		if target.Adapter.Manifest.Name == source.Adapter.Manifest.Name && target.Profile == source.Profile {
			return reportError(stderr, errors.New("source and target are the same browser profile"))
		}
		bundle, err := exportBrowserResource(resolver, source, resource, request, stderr)
		if err != nil {
			return reportError(stderr, err)
		}
		if err := invokeBrowserShareJSON(resolver, target, resource, "import", browserResourceRequest{Version: browsershare.Version, Args: adapterArgs, Bundle: &bundle, Replace: *replace}, nil, stderr); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "shared %s into %s\n", resource, *toProfile)
		return 0
	}
	if *toProfile != "" || (*toFile == "") == !*toStdout {
		return reportErrorCode(stderr, errors.New("resource export needs exactly one of --to-file or --stdout"), 2)
	}
	if *toStdout {
		if err := requireOutputPipe(stdout); err != nil {
			return reportErrorCode(stderr, err, 2)
		}
	} else if err := requireNewOutputFile(*toFile); err != nil {
		return reportError(stderr, err)
	}
	bundle, err := exportBrowserResource(resolver, source, resource, request, stderr)
	if err != nil {
		return reportError(stderr, err)
	}
	if *toStdout {
		if err := json.NewEncoder(stdout).Encode(bundle); err != nil {
			return reportError(stderr, err)
		}
		return 0
	}
	if err := writePrivateJSON(*toFile, bundle); err != nil {
		return reportError(stderr, err)
	}
	fmt.Fprintf(stdout, "exported %s into %s (mode 0600)\n", resource, *toFile)
	return 0
}

func exportBrowserResource(resolver *config.Resolver, source browserEndpoint, resource string, request browserResourceRequest, stderr io.Writer) (browserResourceBundle, error) {
	var bundle browserResourceBundle
	if err := invokeBrowserShareJSON(resolver, source, resource, "export", request, &bundle, stderr); err != nil {
		return bundle, err
	}
	if err := validateBrowserResourceBundle(bundle, resource); err != nil {
		return bundle, err
	}
	bundle.Source = source.Adapter.Manifest.Name + ":" + source.Profile
	return bundle, nil
}

func importBrowserResource(resolver *config.Resolver, resource, fromFile, toProfile string, replace bool, adapterArgs []string, stdout, stderr io.Writer) int {
	target, err := parseBrowserEndpoint(toProfile)
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	if !target.Adapter.HasBrowserShare(resource + ".import") {
		return reportError(stderr, fmt.Errorf("%s adapter cannot import %s", target.Adapter.Manifest.Name, resource))
	}
	var input io.Reader = os.Stdin
	if fromFile != "" {
		file, err := os.Open(fromFile)
		if err != nil {
			return reportError(stderr, err)
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
			return reportErrorCode(stderr, errors.New("resource bundle must be a regular file under 16 MiB"), 2)
		}
		input = file
	}
	decoder := json.NewDecoder(io.LimitReader(input, 16<<20))
	var bundle browserResourceBundle
	if err := decoder.Decode(&bundle); err != nil {
		return reportErrorCode(stderr, errors.New("invalid resource bundle JSON"), 2)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return reportErrorCode(stderr, errors.New("resource bundle contains trailing data"), 2)
	}
	if err := validateBrowserResourceBundle(bundle, resource); err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	if err := invokeBrowserShareJSON(resolver, target, resource, "import", browserResourceRequest{Version: browsershare.Version, Args: adapterArgs, Bundle: &bundle, Replace: replace}, nil, stderr); err != nil {
		return reportError(stderr, err)
	}
	fmt.Fprintf(stdout, "imported %s into %s\n", resource, toProfile)
	return 0
}

func validateBrowserResourceBundle(bundle browserResourceBundle, resource string) error {
	return browsershare.ValidateResourceBundle(bundle, resource)
}

func requireOutputPipe(stdout io.Writer) error {
	if file, ok := stdout.(*os.File); ok {
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeNamedPipe == 0 {
			return errors.New("--stdout requires a pipe; use --to-file for a protected file")
		}
	}
	return nil
}

func requireNewOutputFile(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return errors.New("output file already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
