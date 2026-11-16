# yyjson 0.12.0

Unmodified MIT-licensed sources from https://github.com/ibireme/yyjson/tree/0.12.0.
The adjacent LICENSE is included in source and binary distributions.

SHA-256:

- `yyjson.c`: `ac2e9bbb2e2d9149d90878d40506a1d624fa0b33c979a11b61075c54782c6d6a`
- `yyjson.h`: `175867c5493a5df648cec566717fa1c29aa2f6096f5f0cf1efad0b65e1f6d7b3`
- `LICENSE`: `45e384d3d52c73cba3a64d6e6c25d47cd738cd8a55c30629e3201046eda62947`

The engine uses strict UTF-8 parsing and preserves JSON number text. Its wire
layer adds duplicate-key checks, envelope validation and depth/frame limits.

CMake force-includes a generated private namespace header for every engine
translation unit. All external `yyjson_api` functions (including `unsafe_`
helpers) are prefixed `ext_host_private_`. The original source files and their
checksums remain unchanged; the build derives names from the pinned header.
This prevents unprefixed parser symbols colliding with a consumer's yyjson.

# miniz 3.0.2 (inflate decoder only)

Unmodified MIT-licensed sources from https://github.com/richgel999/miniz/tree/3.0.2,
in `miniz/`. Only the inflate decoder (`tinfl`) is vendored: the package store
uses it to read zip and gzip-compressed tar archives (`src/archive.c`). The
adjacent `miniz/LICENSE` is included in source and binary distributions.

SHA-256:

- `miniz/miniz_tinfl.c`: `9b7edc1fab708166e10f5e90a618a91c95a91496c4692e38160fdae810c1149a`
- `miniz/miniz_tinfl.h`: `040aad2d9ca23c487652c150277d950be12e72c5453ae1f7c6713ed2efd16b4e`
- `miniz/miniz_common.h`: `8b60e76e726dfcf41a338c3a4c54c70038041a21e6314aa54fd3b7f729ea3412`
- `miniz/LICENSE`: `0115478d567121238cf6cc1c0c361926cf07a49d9e4c9e66da97fac6a01646b3`

`miniz/miniz.h` and `miniz/miniz_export.h` are not upstream files. Upstream
assembles the first from all of its parts and generates the second; here they
are two short headers written for this tree, which give `miniz_tinfl.c` the
declarations it includes.

`src/archive.c` includes `miniz_tinfl.c` into its own translation unit with
`MINIZ_NO_MALLOC`, `MINIZ_NO_STDIO` and `MINIZ_NO_TIME` set and with each of the
four functions it defines renamed `ext_host_private_tinfl_*`, so the decoder
adds no source file to a build and cannot collide with a consumer's miniz or
zlib. The gzip and zip containers, their CRC-32 and the tar
format are engine code, not miniz.
