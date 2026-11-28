# Android network observation

The plain Java model lives in `src/main`. `SystemNetworkObserver` lives in the
Android source set and combines `java.net.NetworkInterface` with
`ConnectivityManager` link properties. Reading routes and DNS servers requires
Android's `ACCESS_NETWORK_STATE` permission; interface data remains available
when the platform permits it.

Observation is bounded in time and cancellable: `NetworkOptions`
carries a timeout (five seconds by default), and
`observe(context, cancellation)` takes a `NetworkCancellation`, which another thread can
`cancel()` and which can carry its own deadline (`withTimeout`). The observer checks before each
interface and network and at the end, and throws `NetworkCancellationException` (a
`CancellationException`) with the code `timeout` or `canceled`.
