//go:build darwin && cgo

package confine

/*
#include <stdint.h>
#include <stdlib.h>

int sandbox_init(const char *profile, uint64_t flags, char **errorbuf);
void sandbox_free_error(char *errorbuf);

static int ext_confine_seatbelt(const char *profile) {
	char *error = NULL;
	int rc = sandbox_init(profile, 0, &error);
	if (error)
		sandbox_free_error(error);
	return rc;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

const seatbeltAvailable = true

// applySeatbelt applies an SBPL profile to the whole process. It is never undone; every thread
// and every program the process executes inherits it. The native error text is not returned: it
// can quote the profile, and with it the grant's paths.
func applySeatbelt(profile string) error {
	text := C.CString(profile)
	defer C.free(unsafe.Pointer(text))
	if C.ext_confine_seatbelt(text) != 0 {
		return fmt.Errorf("%w: sandbox_init", ErrDriver)
	}
	return nil
}
