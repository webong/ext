//go:build !darwin && !linux && !freebsd

package main

import plugin "github.com/webong/ext/pkg/plugin-go/engine"

func watchParent(config) error { return plugin.ErrUnsupported }
