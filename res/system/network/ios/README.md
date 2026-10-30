# ExtNetwork

`ExtNetwork` is the Apple binding for `res/system/network`. It observes local network
interfaces and assigned addresses without opening a connection. On macOS it
reads the kernel routing MIB and SystemConfiguration DNS state directly, with
bounded route allocation and parsing. DNS is reported as partial because the
public service state does not represent the complete supplemental resolver
policy. No subprocess or selectable native tool is used.

iOS route and resolver collections remain explicitly unsupported. Observation
does not request permissions or perform DNS queries.
