//go:build !(darwin || linux)

package plugin

func peakResidentBytes() (uint64, bool) { return 0, false }
