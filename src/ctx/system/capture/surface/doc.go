// Package surface defines portable display and window records, bounded
// capture/input requests and validation. The C ledger observes native surfaces;
// installed Ext resource guests use the Swift and .NET libraries to operate on
// selected windows. DesktopDriver remains a seam for caller-selected desktop
// tools such as X11 and Wayland. This package chooses no native backend.
package surface
