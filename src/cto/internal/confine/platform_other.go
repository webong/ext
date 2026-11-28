//go:build !darwin && !linux

package confine

// The Windows driver (AppContainer and a job object) is the next piece of phase 2; until it
// lands this build offers no driver, so every launch fails with ErrNoDriver.
func platformDrivers() []Driver { return nil }
