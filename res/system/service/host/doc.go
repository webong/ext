// Package host discovers what the local machine provides to programs: shells,
// mounted filesystems, web view runtimes and host resource capacity (CPU, memory,
// storage, power and container state). Discovery reads paths and permissions; it never
// launches what it finds.
//
// The native checks the host-requirements resolver needs (FilesystemAt,
// ExecutableAccess, DirectoryWritable) are exported here, because they are OS
// mechanisms; the resolver itself and the graph vocabulary are in res/graph.
package host
