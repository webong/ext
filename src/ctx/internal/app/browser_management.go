package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/webong/ext/ctx/internal/config"
	"github.com/webong/ext/res/web/contract"
)

func browserCommand(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "manage" {
		fmt.Fprintln(stderr, "ctx: browser requires manage <extension|userscript|bookmarklet|session> <action>")
		return 2
	}
	if len(args) < 3 {
		fmt.Fprintln(stderr, "ctx: browser manage needs a kind and action")
		return 2
	}
	kind, action := args[1], args[2]
	flags := flag.NewFlagSet("browser manage", flag.ContinueOnError)
	flags.SetOutput(stderr)
	target := flags.String("target", "", "browser:profile endpoint; defaults to ctx's browser selection")
	input := flags.String("input", "{}", "operation input as JSON object")
	inputFile := flags.String("input-file", "", "read operation input from a JSON file or stdin using -")
	if err := flags.Parse(args[3:]); err != nil || flags.NArg() != 0 {
		return 2
	}
	if *inputFile != "" && *input != "{}" {
		return reportErrorCode(stderr, errors.New("choose --input or --input-file"), 2)
	}
	var raw json.RawMessage
	if *inputFile != "" {
		var reader io.Reader
		if *inputFile == "-" {
			reader = os.Stdin
		} else {
			file, err := os.Open(*inputFile)
			if err != nil {
				return reportError(stderr, err)
			}
			defer file.Close()
			reader = file
		}
		decoder := json.NewDecoder(io.LimitReader(reader, 8<<20+1))
		if err := decoder.Decode(&raw); err != nil {
			return reportErrorCode(stderr, errors.New("management input must contain one JSON value"), 2)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return reportErrorCode(stderr, errors.New("management input contains trailing data"), 2)
		}
	} else {
		raw = json.RawMessage(*input)
	}
	request := contract.Request{Version: contract.ManagementVersion, Kind: kind, Action: action, Input: raw}
	if err := contract.ValidateRequest(request); err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	endpoint, err := resolveBrowserSource(resolver, strings.TrimSpace(*target))
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	operation := kind + "." + action
	if !endpoint.Adapter.HasBrowserManagement(operation) {
		return reportError(stderr, fmt.Errorf("browser adapter %s does not support management operation %s", endpoint.Adapter.Manifest.Name, operation))
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return reportError(stderr, err)
	}
	var output strings.Builder
	code := invokeAdapterIO(resolver, endpoint.Adapter, "share", endpoint.Profile,
		[]string{"management", kind, action}, "", strings.NewReader(string(encoded)), &output, stderr)
	if code != 0 {
		return code
	}
	var response contract.Response
	if err := json.Unmarshal([]byte(output.String()), &response); err != nil {
		return reportError(stderr, fmt.Errorf("browser adapter returned invalid management response: %w", err))
	}
	if err := contract.ValidateResponse(response, request); err != nil {
		return reportError(stderr, err)
	}
	if _, err := io.WriteString(stdout, output.String()); err != nil {
		return reportError(stderr, err)
	}
	return 0
}
