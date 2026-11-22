//go:build !((darwin || linux || windows) && (amd64 || arm64))

package library

func Supported() bool { return false }

func loadImage(string) (uintptr, error)       { return 0, ErrUnsupported }
func lookup(uintptr, string) (uintptr, error) { return 0, ErrUnsupported }
func register(any, uintptr) error             { return ErrUnsupported }
