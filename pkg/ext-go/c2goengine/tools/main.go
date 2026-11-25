// Command tools translates C libraries with built-in or caller-supplied recipes.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/webong/ext/pkg/ext-go/c2goengine/tools/generate"
	"io"
	"os"
	"strings"
)

type options struct {
	library, recipe, translations        string
	out, outDir, publish, bindings, tags string
}

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintf(os.Stderr, "c2goengine: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, log io.Writer) error {
	var o options
	f := flag.NewFlagSet("c2goengine", flag.ContinueOnError)
	f.SetOutput(log)
	f.StringVar(&o.library, "library", "plugin", "built-in recipe: plugin (default) or ledger")
	f.StringVar(&o.recipe, "recipe", "", "custom JSON recipe; paths resolve relative to this file")
	f.StringVar(&o.translations, "translations", "", "existing Go translation package to stage without running ccgo")
	f.StringVar(&o.out, "out", "", "generated Go file; overrides the recipe default")
	f.StringVar(&o.outDir, "out-dir", "", "generated Go directory; writes generated_<goos>_<goarch>.go")
	f.StringVar(&o.publish, "publish", "", "stage translation and binding files locally for this platform; uploads nothing")
	f.StringVar(&o.bindings, "bindings", "", "override directory of handwritten Go bindings to stage")
	f.StringVar(&o.tags, "tags", "", "comma-separated build tags for existing translations")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", f.Args())
	}
	_, err := generate.Generate(context.Background(), generate.Config{
		Library: o.library, RecipeFile: o.recipe, Translations: o.translations,
		OutputFile: o.out, OutputDir: o.outDir, StageDir: o.publish, BindingsDir: o.bindings,
		Tags: strings.FieldsFunc(o.tags, func(r rune) bool { return r == ',' || r == ' ' }), Log: log,
	})
	return err
}
