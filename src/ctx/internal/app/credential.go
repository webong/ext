package app

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/webong/ctx/src/ctx/internal/config"
	modpkg "github.com/webong/ctx/src/ctx/internal/mod"
)

const maxCredentialBytes = 1024 * 1024

type credentialEndpoint struct {
	adapter *modpkg.Adapter
	item    string
}

// credentialCommand moves only explicitly named items. Secret bytes are never
// accepted as command arguments or written to CTX configuration or the graph.
func credentialCommand(resolver *config.Resolver, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return credentialUsage(stderr)
	}
	action := args[0]
	needed := 1
	if action == "copy" {
		needed = 2
	} else if action != "get" && action != "put" {
		return credentialUsage(stderr)
	}
	if len(args) < needed+1 {
		return credentialUsage(stderr)
	}
	refs := args[1 : needed+1]
	flags := flag.NewFlagSet("credential "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	toFile := flags.String("to-file", "", "write a new private file")
	toStdout := flags.Bool("stdout", false, "write to a pipe")
	fromFile := flags.String("from-file", "", "read a private file")
	fromStdin := flags.Bool("stdin", false, "read from a pipe")
	replace := flags.Bool("replace", false, "replace an existing destination item")
	if err := flags.Parse(args[needed+1:]); err != nil || len(flags.Args()) != 0 {
		return 2
	}
	switch action {
	case "get":
		if (*toFile == "") == !*toStdout || *fromFile != "" || *fromStdin || *replace {
			return credentialUsage(stderr)
		}
		if *toStdout && !credentialPipe(stdout) {
			fmt.Fprintln(stderr, "ctx: credential --stdout requires a pipe")
			return 2
		}
		if *toFile != "" {
			if _, err := os.Lstat(*toFile); err == nil {
				fmt.Fprintln(stderr, "ctx: credential output file already exists")
				return 1
			} else if !errors.Is(err, os.ErrNotExist) {
				return reportError(stderr, err)
			}
		}
		source, err := loadCredentialEndpoint(refs[0])
		if err != nil {
			return reportErrorCode(stderr, err, 2)
		}
		reportCredentialRequester(source, stderr)
		value, code := readCredential(resolver, source, stderr)
		if code != 0 {
			return code
		}
		defer eraseCredential(value)
		if *toStdout {
			if _, err := stdout.Write(value); err != nil {
				return reportError(stderr, err)
			}
			return 0
		}
		if err := writePrivateOutput(*toFile, func(output io.Writer) error {
			_, err := output.Write(value)
			return err
		}); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "wrote credential to %s\n", *toFile)
		return 0
	case "put":
		if (*fromFile == "") == !*fromStdin || *toFile != "" || *toStdout {
			return credentialUsage(stderr)
		}
		if *fromStdin && !credentialInputAllowed(stdin) {
			fmt.Fprintln(stderr, "ctx: credential --stdin requires redirected input")
			return 2
		}
		target, err := loadCredentialEndpoint(refs[0])
		if err != nil {
			return reportErrorCode(stderr, err, 2)
		}
		var input io.Reader = stdin
		if *fromFile != "" {
			file, err := openPrivateCredentialFile(*fromFile)
			if err != nil {
				return reportError(stderr, err)
			}
			defer file.Close()
			input = file
		}
		value, err := io.ReadAll(io.LimitReader(input, maxCredentialBytes+1))
		if err != nil || len(value) == 0 || len(value) > maxCredentialBytes {
			return reportErrorCode(stderr, errors.New("credential input must contain 1 byte to 1 MiB"), 2)
		}
		defer eraseCredential(value)
		reportCredentialRequester(target, stderr)
		if code := writeCredential(resolver, target, value, *replace, stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "stored credential in %s\n", target.adapter.Manifest.Name)
		return 0
	case "copy":
		if *toFile != "" || *toStdout || *fromFile != "" || *fromStdin || refs[0] == refs[1] {
			return credentialUsage(stderr)
		}
		source, err := loadCredentialEndpoint(refs[0])
		if err != nil {
			return reportErrorCode(stderr, err, 2)
		}
		target, err := loadCredentialEndpoint(refs[1])
		if err != nil {
			return reportErrorCode(stderr, err, 2)
		}
		value, code := readCredential(resolver, source, stderr)
		if code != 0 {
			return code
		}
		defer eraseCredential(value)
		if code := writeCredential(resolver, target, value, *replace, stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "copied credential from %s to %s\n", source.adapter.Manifest.Name, target.adapter.Manifest.Name)
		return 0
	}
	return credentialUsage(stderr)
}

func credentialUsage(stderr io.Writer) int {
	fmt.Fprintln(stderr, "ctx: use credential get <adapter:item> (--to-file <path> | --stdout), put <adapter:item> (--from-file <path> | --stdin) [--replace], or copy <source> <target> [--replace]")
	return 2
}

func loadCredentialEndpoint(ref string) (credentialEndpoint, error) {
	name, item, ok := strings.Cut(ref, ":")
	if !ok || name == "" || item == "" || len(item) > 2048 || strings.ContainsAny(item, "\x00\r\n") {
		return credentialEndpoint{}, errors.New("credential reference must be adapter:item")
	}
	adapter, err := adapterStore().Load(name)
	if err != nil || !adapter.HasCapability("share") || !containsShareSpace(adapter.Manifest.ShareSpaces, "credential") {
		return credentialEndpoint{}, fmt.Errorf("credential adapter %s is unavailable", name)
	}
	if adapter.Manifest.APIVersion != modpkg.APIVersion {
		return credentialEndpoint{}, fmt.Errorf("credential adapter %s requires API %s", name, modpkg.APIVersion)
	}
	if err := adapterStore().AssertTrusted(adapter); err != nil {
		return credentialEndpoint{}, err
	}
	return credentialEndpoint{adapter: adapter, item: item}, nil
}

// The inherited adapter name is only an informational hint. The display label
// comes from a trusted manifest and never changes authorization or native UI.
func reportCredentialRequester(endpoint credentialEndpoint, stderr io.Writer) {
	name := os.Getenv("CTX_ADAPTER_NAME")
	if name == "" || name == endpoint.adapter.Manifest.Name {
		return
	}
	store := adapterStore()
	requester, err := store.Load(name)
	if err != nil || store.AssertTrusted(requester) != nil {
		return
	}
	dependency, declared := requester.Manifest.Dependencies["credential"]
	if !declared || dependency.Adapter != endpoint.adapter.Manifest.Name {
		return
	}
	fmt.Fprintf(stderr, "ctx: reported requester %s (%s) is accessing credentials through %s; check any native permission prompt separately\n",
		requester.DisplayLabel(), requester.Manifest.Name, endpoint.adapter.Manifest.Name)
}

type boundedCredentialWriter struct{ bytes.Buffer }

func (writer *boundedCredentialWriter) Write(value []byte) (int, error) {
	if writer.Len()+len(value) > maxCredentialBytes {
		return 0, errors.New("credential exceeds 1 MiB")
	}
	return writer.Buffer.Write(value)
}

func readCredential(resolver *config.Resolver, source credentialEndpoint, stderr io.Writer) ([]byte, int) {
	var output boundedCredentialWriter
	code := invokeAdapterIO(resolver, source.adapter, "share", "", []string{"credential", "get", source.item}, "", bytes.NewReader(nil), &output, stderr)
	if code != 0 {
		return nil, code
	}
	if output.Len() == 0 {
		return nil, reportError(stderr, errors.New("credential adapter returned an empty item"))
	}
	return output.Bytes(), 0
}

func writeCredential(resolver *config.Resolver, target credentialEndpoint, value []byte, replace bool, stderr io.Writer) int {
	args := []string{"credential", "put", target.item}
	if replace {
		args = append(args, "--replace")
	}
	return invokeAdapterIO(resolver, target.adapter, "share", "", args, "", bytes.NewReader(value), io.Discard, stderr)
}

func eraseCredential(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func credentialPipe(output io.Writer) bool {
	file, ok := output.(*os.File)
	if !ok {
		return true // Tests and embedded callers control their writer.
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeNamedPipe != 0
}

func credentialInputAllowed(input io.Reader) bool {
	file, ok := input.(*os.File)
	if !ok {
		return true // Tests and embedded callers control their reader.
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice == 0
}

func openPrivateCredentialFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("credential input must be a private regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || runtime.GOOS != "windows" && opened.Mode().Perm()&0o077 != 0 {
		file.Close()
		return nil, errors.New("credential input changed while opening")
	}
	return file, nil
}
