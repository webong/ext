// Package packages validates, resolves and verifies ext.package/v1 manifests
// (packagekit) with the plugin engine: on the shared C engine when built with
// the ext_cengine tag and cgo, otherwise in pure Go. It is separate from the
// root goengine package so a program that only needs sessions and guests does
// not pull in packagekit and its dependencies.
package packages
