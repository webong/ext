// Package symbols holds the yaegi exports of the ext guest packages: the
// engine's root package, its guest packages, its in-process binding and its
// schema types. The files
// beside this one are generated; run go generate after the engine's exported
// API changes (TestSymbolsAreCurrent fails until then).
package symbols

import "reflect"

//go:generate go run github.com/traefik/yaegi/cmd/yaegi extract -name symbols github.com/webong/ext/pkg/ext-go/engine github.com/webong/ext/pkg/ext-go/engine/guest/author github.com/webong/ext/pkg/ext-go/engine/guest/capability github.com/webong/ext/pkg/ext-go/engine/guest/instance github.com/webong/ext/pkg/ext-go/engine/guest/stream github.com/webong/ext/pkg/ext-go/engine/transport/inprocess github.com/webong/ext/pkg/ext-go/engine/validate/schema

// Symbols maps "<import path>/<package name>" to the package's exports.
var Symbols = map[string]map[string]reflect.Value{}
