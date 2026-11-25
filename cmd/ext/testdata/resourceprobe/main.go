// resourceprobe fails discovery acceptance if a manifest-only operation launches it.
package main

import "os"

var marker string

func main() {
	if path := marker; path != "" {
		_ = os.WriteFile(path, []byte("provider was launched"), 0600)
	}
	os.Exit(99)
}
