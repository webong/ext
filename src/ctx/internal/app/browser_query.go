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

	"github.com/webong/ext/ctx/internal/app/browserhost"
	"github.com/webong/ext/ctx/internal/config"
	"github.com/webong/ext/res/browser"
)

type browserQueryList []string

func (values *browserQueryList) String() string { return fmt.Sprint([]string(*values)) }
func (values *browserQueryList) Set(value string) error {
	if value == "" {
		return errors.New("query option cannot be empty")
	}
	*values = append(*values, value)
	return nil
}

func shareBrowserCookieQuery(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("share:browser cookie query", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var sites, sources, browsers, names browserQueryList
	flags.Var(&sites, "site", "site URL (repeatable)")
	flags.Var(&sources, "from", "browser:profile source (repeatable, ordered)")
	flags.Var(&browsers, "browser", "browser adapter to discover (repeatable, ordered)")
	flags.Var(&names, "name", "cookie name (repeatable)")
	mode := flags.String("mode", string(browser.ModeMerge), "merge or first")
	format := flags.String("format", "json", "json, header, or netscape")
	toFile := flags.String("to-file", "", "new protected result file")
	toStdout := flags.Bool("stdout", false, "write result to a pipe")
	inlineFile := flags.String("inline-file", "", "JSON or Netscape cookie file used first")
	inlineStdin := flags.Bool("inline-stdin", false, "read JSON or Netscape cookies from a pipe")
	fallbackFile := flags.String("fallback-file", "", "JSON or Netscape cookie file used after browsers")
	fallbackStdin := flags.Bool("fallback-stdin", false, "read fallback cookies from a pipe")
	inlineOnly := flags.Bool("inline-only", false, "query inline cookies without browser adapters")
	includeExpired := flags.Bool("include-expired", false, "include expired cookies")
	allHosts := flags.Bool("all-hosts", false, "allow queries without a site URL")
	strict := flags.Bool("strict", false, "fail without output when a source reports a warning")
	requireMatch := flags.Bool("require-match", false, "fail without output when no cookies match")
	timeout := flags.Duration("timeout", 2*time.Minute, "overall adapter query timeout")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 {
		return 2
	}
	if *format != "json" && *format != "header" && *format != "netscape" {
		return reportErrorCode(stderr, errors.New("cookie query format must be json, header, or netscape"), 2)
	}
	if *format == "header" && (len(sites) != 1 || *allHosts || *includeExpired) {
		return reportErrorCode(stderr, errors.New("header output needs exactly one --site and active cookies"), 2)
	}
	if (*toFile == "") == !*toStdout || (*inlineFile != "" && *inlineStdin) ||
		(*fallbackFile != "" && *fallbackStdin) || (*inlineStdin && *fallbackStdin) {
		fmt.Fprintln(stderr, "ctx: query needs --to-file or --stdout, one source per inline input, and only one stdin source")
		return 2
	}
	if *toStdout {
		if err := requireOutputPipe(stdout); err != nil {
			return reportErrorCode(stderr, err, 2)
		}
	} else if err := requireNewOutputFile(*toFile); err != nil {
		return reportError(stderr, err)
	}
	options := browser.Options{
		Sources: append([]string(nil), sources...), Browsers: append([]string(nil), browsers...),
		Names: append([]string(nil), names...), Mode: browser.Mode(*mode),
		InlineOnly: *inlineOnly, IncludeExpired: *includeExpired, AllowAllHosts: *allHosts,
		Timeout: *timeout,
		Backend: browserhost.Provider{},
	}
	if len(sites) > 0 {
		options.URL = sites[0]
		options.Origins = append([]string(nil), sites[1:]...)
	}
	if len(sources) == 0 && len(browsers) == 0 && !*inlineOnly {
		options.PreferredSource = os.Getenv("CTX_BROWSER")
		if options.PreferredSource == "" {
			if resolver != nil {
				if selected, err := resolver.Resolve("browser"); err == nil {
					options.PreferredSource = selected.Value
				}
			}
		}
	}
	if *inlineFile != "" {
		options.Inline.File = *inlineFile
	}
	if *fallbackFile != "" {
		options.FallbackInline.File = *fallbackFile
	}
	if *inlineStdin || *fallbackStdin {
		file, err := os.Stdin.Stat()
		if err != nil {
			return reportErrorCode(stderr, err, 2)
		}
		if file.Mode()&os.ModeNamedPipe == 0 {
			inputFlag := "--inline-stdin"
			if *fallbackStdin {
				inputFlag = "--fallback-stdin"
			}
			return reportErrorCode(stderr, fmt.Errorf("%s requires a pipe", inputFlag), 2)
		}
		data, err := io.ReadAll(io.LimitReader(os.Stdin, (8<<20)+1))
		if err != nil || len(data) > 8<<20 {
			return reportErrorCode(stderr, errors.New("inline cookie input exceeds 8 MiB or cannot be read"), 2)
		}
		if *inlineStdin {
			options.Inline.Data = data
		} else {
			options.FallbackInline.Data = data
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	result, err := browser.Get(ctx, options)
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(stderr, "ctx: warning: %s\n", warning)
	}
	if *strict && len(result.Warnings) > 0 {
		return reportError(stderr, errors.New("cookie query was incomplete; no output written (--strict)"))
	}
	if *requireMatch && len(result.Cookies) == 0 {
		return reportError(stderr, errors.New("no cookies matched; no output written (--require-match)"))
	}
	var encoded []byte
	switch *format {
	case "json":
		encoded, err = json.Marshal(result)
	case "header":
		var header string
		header, err = browser.CookieHeader(result.Cookies, sites[0])
		if header != "" {
			encoded = []byte("Cookie: " + header)
		}
	case "netscape":
		var jar string
		jar, err = browser.NetscapeCookies(result.Cookies)
		encoded = []byte(jar)
	}
	if err != nil {
		return reportError(stderr, err)
	}
	if *format != "netscape" {
		encoded = append(encoded, '\n')
	}
	if *toStdout {
		_, err = stdout.Write(encoded)
	} else {
		err = writePrivateOutput(*toFile, func(output io.Writer) error {
			_, err := output.Write(encoded)
			return err
		})
	}
	if err != nil {
		return reportError(stderr, err)
	}
	if *toStdout {
		return 0
	}
	fmt.Fprintf(stdout, "wrote %d cookies to %s (mode 0600)\n", len(result.Cookies), *toFile)
	return 0
}
