//go:build !darwin && !linux && !freebsd

package main

import "github.com/webong/ext/pkg/plugin"

func watchParent(config) error { return plugin.ErrUnsupported }
