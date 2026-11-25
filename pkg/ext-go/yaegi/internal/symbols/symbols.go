// Package symbols holds the yaegi exports of the ext guest packages: the
// engine's root package, its guest packages, its in-process binding and its
// schema types. The files
// beside this one are generated; run go generate after the engine's exported
// API changes (TestSymbolsAreCurrent fails until then).
package symbols

import "reflect"

//go:generate go run github.com/webong/ext/pkg/ext-go/yaegi/internal/symbols/gen

// Symbols maps "<import path>/<package name>" to the package's exports.
var Symbols = map[string]map[string]reflect.Value{}
