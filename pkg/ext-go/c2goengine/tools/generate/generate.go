// Package generate provides build-time C-to-Go translation and local staging.
// It belongs to the tools module; runtime applications need only the generated
// package and its runtime dependencies, not this generator.
package generate

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Config selects one recipe or an existing Go translation package.
// Relative paths resolve from BaseDir (the caller's cwd when empty). A recipe
// file's own paths resolve from its directory instead. No setting changes cwd.
type Config struct {
	BaseDir string
	// Library selects plugin (default) or ledger when no custom input is supplied.
	Library string
	// Repository optionally supplies the Ext checkout for a built-in recipe.
	Repository string
	// Recipe and RecipeFile are alternative ways to supply a custom recipe.
	Recipe     *Recipe
	RecipeFile string
	// Translations stages an existing package without invoking the compiler.
	Translations string
	// OutputFile and OutputDir are mutually exclusive. OutputDir uses a platform filename.
	OutputFile string
	OutputDir  string
	// StageDir writes platform-selected Go files locally; it uploads nothing.
	StageDir string
	// BindingsDir overrides the recipe's handwritten Go binding directory for staging.
	BindingsDir string
	// Tags select files from Translations. Generation uses the recipe's build tag.
	Tags []string
	// Log receives generation diagnostics. Nil discards them.
	Log io.Writer
}

// Result identifies generated and staged artifacts. Reuse leaves OutputFile empty.
type Result struct {
	OutputFile  string
	StageDir    string
	StagedFiles int
	// InputDigest is supplied by the built-in plugin recipe.
	InputDigest string
}

// Generate runs the selected workflow. Explicit invalid inputs return an error;
// only omitted inputs select defaults. It does not modify Config or its Recipe.
// Calls may run concurrently when their source/output directories do not overlap.
func Generate(ctx context.Context, c Config) (Result, error) {
	var result Result
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if c.Log == nil {
		c.Log = io.Discard
	}
	base := c.BaseDir
	if base == "" {
		var err error
		base, err = os.Getwd()
		if err != nil {
			return result, err
		}
	}
	base, err := filepath.Abs(base)
	if err != nil {
		return result, err
	}
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	c.RecipeFile = resolve(c.RecipeFile)
	c.Translations = resolve(c.Translations)
	c.OutputFile = resolve(c.OutputFile)
	c.OutputDir = resolve(c.OutputDir)
	c.StageDir = resolve(c.StageDir)
	c.BindingsDir = resolve(c.BindingsDir)
	c.Repository = resolve(c.Repository)
	if c.Library == "" {
		c.Library = "plugin"
	}
	if c.OutputFile != "" && c.OutputDir != "" {
		return result, fmt.Errorf("choose OutputFile or OutputDir, not both")
	}
	inputs := 0
	if c.Recipe != nil {
		inputs++
	}
	if c.RecipeFile != "" {
		inputs++
	}
	if c.Translations != "" {
		inputs++
	}
	if inputs > 1 {
		return result, fmt.Errorf("choose Recipe, RecipeFile, or Translations, not more than one")
	}
	if c.Translations != "" {
		if c.OutputFile != "" || c.OutputDir != "" || c.BindingsDir != "" {
			return result, fmt.Errorf("Translations uses the supplied package as-is; use StageDir for its destination")
		}
		if c.StageDir == "" {
			return result, fmt.Errorf("Translations requires StageDir")
		}
		if err := separatePaths(c.Translations, c.StageDir); err != nil {
			return result, err
		}
		n, err := publishTaggedTranslation(c.Translations, c.StageDir, runtime.GOOS, runtime.GOARCH, strings.Join(c.Tags, ","))
		if err != nil {
			return result, err
		}
		result.StageDir, result.StagedFiles = c.StageDir, n
		fmt.Fprintf(c.Log, "staged %d existing files in %s\n", n, c.StageDir)
		return result, nil
	}
	if len(c.Tags) > 0 {
		return result, fmt.Errorf("Tags applies only to Translations; generation uses the recipe's build tag")
	}
	if c.BindingsDir != "" && c.StageDir == "" {
		return result, fmt.Errorf("BindingsDir requires StageDir")
	}
	var root, tag, defaults string
	var custom *Recipe
	if c.RecipeFile != "" {
		custom, err = LoadRecipe(c.RecipeFile)
	} else if c.Recipe != nil {
		recipePath := c.Recipe.path
		if recipePath == "" {
			recipePath = filepath.Join(base, "recipe.json")
		}
		custom, err = normalizeRecipe(*c.Recipe, recipePath, true)
	}
	if err != nil {
		return result, err
	}
	if custom != nil {
		defaults = filepath.Join(filepath.Dir(custom.path), "generated")
		tag = custom.BuildTag
		if c.BindingsDir == "" {
			c.BindingsDir = custom.bindingsPath()
		}
	} else {
		if c.Library != "plugin" && c.Library != "ledger" {
			return result, fmt.Errorf("unknown built-in library %q; supply a recipe for a custom library", c.Library)
		}
		root = c.Repository
		if root == "" {
			root, err = repositoryRoot(base)
			if err != nil {
				return result, err
			}
		}
		defaults = filepath.Join(root, "pkg/ext-go/c2goengine", c.Library, "internal/ccgo")
		tag = "ext_c2go"
		if c.Library == "ledger" {
			tag = "ext_ledger_c2go"
		}
		if c.BindingsDir == "" {
			c.BindingsDir = defaults
		}
	}
	if c.OutputFile == "" {
		if c.OutputDir == "" {
			c.OutputDir = defaults
		}
		c.OutputFile = filepath.Join(c.OutputDir, fmt.Sprintf("generated_%s_%s.go", runtime.GOOS, runtime.GOARCH))
	}
	if custom != nil {
		err = generateCustom(ctx, c.Log, custom, c.OutputFile)
	} else if c.Library == "ledger" {
		err = generateLedger(ctx, c.Log, root, c.OutputFile)
	} else {
		result.InputDigest, err = generatePlugin(ctx, c.Log, root, c.OutputFile)
		if err == nil {
			fmt.Fprintf(c.Log, "inputs %s\n", result.InputDigest[:12])
		}
	}
	if err != nil {
		return result, err
	}
	result.OutputFile = c.OutputFile
	fmt.Fprintf(c.Log, "wrote %s\n", c.OutputFile)
	if c.StageDir != "" {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		n, err := stageGenerated(c.BindingsDir, c.OutputFile, c.StageDir, tag)
		if err != nil {
			return result, err
		}
		result.StageDir, result.StagedFiles = c.StageDir, n
		fmt.Fprintf(c.Log, "staged %d files for %s/%s in %s\n", n, runtime.GOOS, runtime.GOARCH, c.StageDir)
	}
	return result, nil
}
