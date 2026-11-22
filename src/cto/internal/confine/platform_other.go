//go:build !darwin

package confine

// The Linux (Landlock and seccomp) and Windows (AppContainer and a job object) drivers are the
// next pieces of phase 2; until they land this build offers no driver, so every launch fails
// with ErrNoDriver.
func platformDrivers() []Driver { return nil }
