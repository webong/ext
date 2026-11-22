//go:build darwin && !cgo

package confine

// Without cgo there is no sandbox_init: the driver is listed and enforces nothing.
const seatbeltAvailable = false

func applySeatbelt(string) error { return ErrNoDriver }
