//go:build !(darwin || linux)

package c2goengine

func peakResidentBytes() (uint64, bool) { return 0, false }
