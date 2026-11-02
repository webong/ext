# Android host library

`SystemResourcesObserver` reports host resources in the same bounded model as the Go
and Swift contracts. Memory observation distinguishes physical memory
(`memory_bytes`, from `ActivityManager.MemoryInfo.totalMem`) from effective capacity
(`memory_capacity_bytes`, equal to physical on Android because no container memory limit
is exposed at this level). A missing `ActivityManager` or zero total memory yields a
non-complete memory status rather than guessing host values. Storage and battery absence
are reported explicitly.
