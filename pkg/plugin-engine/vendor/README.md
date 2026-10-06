# yyjson 0.12.0

Unmodified MIT-licensed sources from https://github.com/ibireme/yyjson/tree/0.12.0.
The adjacent LICENSE is included in source and binary distributions.

SHA-256:

- `yyjson.c`: `ac2e9bbb2e2d9149d90878d40506a1d624fa0b33c979a11b61075c54782c6d6a`
- `yyjson.h`: `175867c5493a5df648cec566717fa1c29aa2f6096f5f0cf1efad0b65e1f6d7b3`
- `LICENSE`: `45e384d3d52c73cba3a64d6e6c25d47cd738cd8a55c30629e3201046eda62947`

CTX uses strict UTF-8 parsing and preserves JSON number text. CTX's own wire
layer adds duplicate-key checks, envelope validation and depth/frame limits.

CMake force-includes a generated private namespace header for every engine
translation unit. All external `yyjson_api` functions (including `unsafe_`
helpers) are prefixed `ctx_host_private_`. The original source files and their
checksums remain unchanged; the build derives names from the pinned header.
This prevents unprefixed parser symbols colliding with a consumer's yyjson.
