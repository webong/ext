# Generator

`go run .` (with `GOWORK=off`, from this directory) writes the Go translation of the C engine's
portable core for the platform it runs on. See [../README.md](../README.md#regenerating).

This is its own module so that ccgo and its dependencies stay out of every consumer's module graph.
