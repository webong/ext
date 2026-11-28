//go:build darwin

package confine

// platformGuest performs the operations only some platforms' tests use.
func platformGuest([]string) (error, bool) { return nil, false }
