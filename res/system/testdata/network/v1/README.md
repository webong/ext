# network v1

`cases.json` covers the pure rules of the network observation contract. The three languages each
observe the machine their own way, so only what they should agree on is here.

- **Options**: each of `maxInterfaces`, `maxAddresses` and `maxRoutes` is 1 to 65,536, else
  `invalid`. The defaults are 256, 256 and 4096. Go reads a zero as the default and cannot tell it
  from an absent value, so zero is not a case.
- **Addresses**: an address is built from a literal IPv4 or IPv6 address and a prefix length. Its text
  is the canonical form: IPv4 as dotted decimal, IPv6 as RFC 5952 (lower case, no leading zeros,
  the longest run of two or more zero groups compressed, the first when they tie). A zone, a
  bracket, a leading zero in an IPv4 octet, a host name and a prefix beyond 32 (IPv4) or 128 (IPv6)
  are `invalid`. IPv4-mapped IPv6 addresses are not cases.
- **Scopes** follow the IANA special-purpose registries: `loopback` is 127.0.0.0/8 and ::1;
  `unspecified` is 0.0.0.0 and ::; `link` is 169.254.0.0/16, fe80::/10, 224.0.0.0/24 and IPv6
  multicast with scope 2 (ffX2::); `private` is 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 and
  fc00::/7; `multicast` is the rest of 224.0.0.0/4 and ff00::/8; everything else, including
  100.64.0.0/10 and the documentation ranges, is `global`.
- **Resolver**: each of the three lists is trimmed (strip leading and trailing U+0009 to U+000D and
  U+0020), empty entries are dropped, and a repeated entry is dropped (the first stays). The order
  the system reported is kept, because the order of name servers and of search domains is
  meaningful. Case is kept.

Not covered, because it is each platform's own observation: the interfaces, routes and resolver
the system reports, flags, hardware addresses, the order of interfaces and routes, and the
collection time limit (only Go has one).
