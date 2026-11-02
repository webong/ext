// Package surface observes the local display, window, accessibility and permission
// state, and captures a window and sends atomic input to one. Targets are explicitly
// observed values whose identity is revalidated before an operation; no handle of the
// native toolkit is exposed. The DesktopDriver interface is the seam that caller-selected
// tools implement where the OS does not allow a direct implementation (for example
// Wayland), and the package never selects a driver by name.
package surface
