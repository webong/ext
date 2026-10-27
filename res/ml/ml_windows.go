//go:build windows

package ml

// platformProvider reports that native Windows ML (WinML) inference is not
// implemented in the Go module; the WinML backend ships in res/ml/windows as
// part of the Ext.System.Windows C# target.
func platformProvider() (Provider, error) {
	return nil, ErrUnavailable
}
