package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/webong/ext/res/system/clipboard"
)

func clipboardCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctx: clipboard needs generation, read, write, or clear")
		return 2
	}
	action := args[0]
	backend, available := nativeClipboardBackend()
	if !available {
		fmt.Fprintln(stderr, "ctx: no Go clipboard backend is available on this platform; Apple and Android bindings ship in the SDKs")
		return 2
	}
	formatName := ""
	expected := ""
	text := ""
	file := ""
	maxBytes := int64(16 << 20)
	jsonOutput := false
	for index := 1; index < len(args); index++ {
		argument := args[index]
		name, inline, hasInline := strings.Cut(argument, "=")
		value := func() (string, error) {
			if hasInline {
				return inline, nil
			}
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return "", fmt.Errorf("%s needs a value", name)
			}
			index++
			return args[index], nil
		}
		var err error
		switch name {
		case "--format":
			formatName, err = value()
		case "--generation":
			expected, err = value()
		case "--text":
			text, err = value()
		case "--file":
			file, err = value()
		case "--max-bytes":
			var raw string
			raw, err = value()
			if err == nil {
				maxBytes, err = strconv.ParseInt(raw, 10, 64)
			}
		case "--json":
			if hasInline {
				err = fmt.Errorf("--json takes no value")
			} else {
				jsonOutput = true
			}
		default:
			err = fmt.Errorf("unknown clipboard option %s", name)
		}
		if err != nil {
			fmt.Fprintf(stderr, "ctx: %v\n", err)
			return 2
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch action {
	case "generation":
		if formatName != "" || expected != "" || text != "" || file != "" {
			fmt.Fprintln(stderr, "ctx: clipboard generation takes no options")
			return 2
		}
		generation, err := backend.Generation(ctx)
		if err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintln(stdout, generation)
		return 0
	case "read":
		if formatName == "" || text != "" || file != "" || expected != "" {
			fmt.Fprintln(stderr, "ctx: clipboard read needs --format and no write options")
			return 2
		}
		policy := clipboard.Policy{MaxBytes: maxBytes, AllowedFormats: []string{formatName}, AllowRead: true}
		reader, err := clipboard.New(backend, policy)
		if err != nil {
			return reportError(stderr, err)
		}
		snapshot, err := reader.Read(ctx)
		if err != nil {
			return reportError(stderr, err)
		}
		if jsonOutput {
			return writeJSON(stdout, stderr, snapshot)
		}
		for _, item := range snapshot.Items {
			for _, representation := range item.Representations {
				fmt.Fprintf(stdout, "%s\t%d bytes\n", representation.Format, len(representation.Data))
			}
		}
		return 0
	case "write":
		if formatName == "" || expected == "" || (text == "" && file == "") || (text != "" && file != "") {
			fmt.Fprintln(stderr, "ctx: clipboard write needs --format, --generation, and exactly one of --text or --file")
			return 2
		}
		data := []byte(text)
		if file != "" {
			payload, err := readBounded(file, maxBytes)
			if err != nil {
				return reportError(stderr, err)
			}
			data = payload
		}
		policy := clipboard.Policy{MaxBytes: maxBytes, AllowedFormats: []string{formatName}, AllowWrite: true}
		writer, err := clipboard.New(backend, policy)
		if err != nil {
			return reportError(stderr, err)
		}
		receipt, err := writer.Write(ctx, clipboard.WriteRequest{
			ExpectedGeneration: expected,
			Items:              []clipboard.Item{{Representations: []clipboard.Representation{{Format: formatName, Data: data}}}},
		})
		if err != nil {
			return reportError(stderr, err)
		}
		return writeJSON(stdout, stderr, receipt)
	case "clear":
		if expected == "" || formatName != "" || text != "" || file != "" {
			fmt.Fprintln(stderr, "ctx: clipboard clear needs --generation and no other options")
			return 2
		}
		policy := clipboard.Policy{MaxBytes: maxBytes, AllowedFormats: []string{"CF_UNICODETEXT"}, AllowWrite: true}
		writer, err := clipboard.New(backend, policy)
		if err != nil {
			return reportError(stderr, err)
		}
		receipt, err := writer.Clear(ctx, expected)
		if err != nil {
			return reportError(stderr, err)
		}
		return writeJSON(stdout, stderr, receipt)
	}
	fmt.Fprintf(stderr, "ctx: unknown clipboard action %s\n", action)
	return 2
}

func readBounded(path string, maximum int64) ([]byte, error) {
	if maximum <= 0 {
		return nil, fmt.Errorf("clipboard byte bound must be positive")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("clipboard file exceeds the byte bound of %d", maximum)
	}
	return data, nil
}
