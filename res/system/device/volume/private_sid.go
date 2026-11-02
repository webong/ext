package volume

// broadWriteSIDs are the well-known Windows identities whose write access makes
// a directory non-private. The check is by SID string so it can be reasoned about
// and tested on every platform; only the Windows backend reads a DACL.
var broadWriteSIDs = map[string]bool{
	"S-1-1-0":      true, // Everyone
	"S-1-5-7":      true, // Anonymous
	"S-1-5-11":     true, // Authenticated Users
	"S-1-5-32-545": true, // Users
	"S-1-5-32-546": true, // Guests
}

func broadWriteSID(sid string) bool { return broadWriteSIDs[sid] }
