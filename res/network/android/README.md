# Android network observation

The plain Java model lives in `src/main`. `SystemNetworkObserver` lives in the
Android source set and combines `java.net.NetworkInterface` with
`ConnectivityManager` link properties. Reading routes and DNS servers requires
Android's `ACCESS_NETWORK_STATE` permission; interface data remains available
when the platform permits it.
