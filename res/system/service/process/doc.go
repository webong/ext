// Package process discovers and inspects running processes. A snapshot carries
// process identity (pid, start identity, parent), the executable and owner, and, on
// request, bounded file, mapping, socket and IPC evidence and usage. Coverage says
// whether an empty answer means nothing was there or the OS did not allow looking.
// Arguments, environment contents and file contents are never collected.
package process
