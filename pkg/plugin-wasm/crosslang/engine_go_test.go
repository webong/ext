//go:build !ctx_cengine

package crosslang_test

import (
	"github.com/webong/ctx/pkg/plugin/plugintest"
	"testing"
)

func runGuestConformance(t *testing.T, factory plugintest.Factory) { plugintest.Run(t, factory) }
