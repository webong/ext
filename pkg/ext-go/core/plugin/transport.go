package plugin

// JSONLineProfile describes the standard JSON-line transport. It starts no guest.
func JSONLineProfile() BackendProfile {
	return BackendProfile{Name: "jsonline", Protocols: []string{APIVersion}, Cancellation: "connection", ProcessOwner: "host", Boundary: BoundaryProcess}
}
