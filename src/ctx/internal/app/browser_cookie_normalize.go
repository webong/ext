package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/webong/ctx/res/browser"
	"github.com/webong/ctx/src/ctx/internal/app/browserhost"
)

func shareBrowserCookieNormalize(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("share:browser cookie normalize", flag.ContinueOnError)
	flags.SetOutput(stderr)
	from := flags.String("from", "", "explicit browser:profile endpoint")
	storeID := flags.String("store-id", "", "native store bound by the exporting application")
	inputFile := flags.String("from-file", "", "authorized native cookie export JSON")
	stdin := flags.Bool("stdin", false, "read native cookie export from a pipe")
	toFile := flags.String("to-file", "", "new protected normalized JSON file")
	toStdout := flags.Bool("stdout", false, "write normalized JSON to a pipe")
	timeout := flags.Duration("timeout", 30*time.Second, "normalization timeout")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return 2
	}
	if *from == "" || *storeID == "" || (*inputFile == "") == !*stdin || (*toFile == "") == !*toStdout {
		return reportErrorCode(stderr, errors.New("normalize needs --from, --store-id, one input (--from-file or --stdin), and one output (--to-file or --stdout)"), 2)
	}
	if *toStdout {
		if err := requireOutputPipe(stdout); err != nil {
			return reportErrorCode(stderr, err, 2)
		}
	} else {
		if err := requireNewOutputFile(*toFile); err != nil {
			return reportError(stderr, err)
		}
	}
	var reader io.Reader
	if *stdin {
		info, err := os.Stdin.Stat()
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
			return reportErrorCode(stderr, errors.New("--stdin requires a pipe"), 2)
		}
		reader = os.Stdin
	} else {
		file, err := os.Open(*inputFile)
		if err != nil {
			return reportError(stderr, err)
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return reportErrorCode(stderr, errors.New("native export input must be a regular file"), 2)
		}
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(reader, browser.MaxCookieInputBytes+1))
	if err != nil || len(data) > browser.MaxCookieInputBytes {
		return reportErrorCode(stderr, errors.New("native export exceeds the input limit or cannot be read"), 2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	result, err := browser.Normalize(ctx, browser.NormalizeOptions{Source: *from, StoreID: *storeID, Export: data, Timeout: *timeout, Backend: browserhost.Provider{}})
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	if *toStdout {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return reportError(stderr, err)
		}
		return 0
	}
	if err := writePrivateJSON(*toFile, result); err != nil {
		return reportError(stderr, err)
	}
	fmt.Fprintf(stdout, "normalized %d cookies to %s (mode 0600)\n", len(result.Cookies), *toFile)
	return 0
}
