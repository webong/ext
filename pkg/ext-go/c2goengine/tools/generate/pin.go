package generate

// The generated code imports the runtime library. Importing it here keeps its version in this
// module's requirements (and its checksums in go.sum), which the generator pins in the
// temporary module it hands to ccgo. libcVersion in shared.go must name the same version.
import (
	_ "modernc.org/ccgo/v4/lib" // pins the worker compiler without its legacy CLI dependencies
	_ "modernc.org/libc"
)
