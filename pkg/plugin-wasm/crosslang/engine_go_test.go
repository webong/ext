//go:build !ext_cengine

package crosslang_test

import (
	"github.com/webong/ext/pkg/plugin/plugintest"
	"testing"
)

func runGuestConformance(t *testing.T, factory plugintest.Factory) { plugintest.Run(t, factory) }
