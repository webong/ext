package contract

import "testing"

func TestWebViewCapabilitiesSeparateCurrentAndFutureExecution(t *testing.T) {
	capabilities := WebViewCapabilities{
		CurrentPage: ScriptExecutionCapability{Supported: true, Frames: []string{FrameMain}, RunAt: []string{RunNow}, Worlds: []string{WorldPage}, RejectsStaleNavigation: true},
		FutureDocuments: ScriptExecutionCapability{Supported: true, Frames: []string{FrameAll}, RunAt: []string{RunDocumentStart}, Worlds: []string{WorldPage}},
	}
	if err := capabilities.Validate(); err != nil {
		t.Fatal(err)
	}
	capabilities.FutureDocuments = ScriptExecutionCapability{}
	if err := capabilities.Validate(); err != nil {
		t.Fatal(err)
	}
	capabilities.FutureDocuments.Frames = []string{FrameAll}
	if err := capabilities.Validate(); err == nil {
		t.Fatal("unsupported path advertised frame behavior")
	}
}
