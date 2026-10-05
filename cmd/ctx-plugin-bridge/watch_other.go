//go:build !darwin && !linux && !freebsd

package main

import "github.com/webong/ctx/pkg/plugin"

func watchParent(config) error { return plugin.ErrUnsupported }
