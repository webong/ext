package goengine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

// runStageGuest is a guest that fails a stage of admission in the way its mode names, written by
// hand: it exits at once, answers the handshake with something else, or never answers it.
func runStageGuest(mode string) int {
	if mode == "stage-exit" {
		return 3
	}
	in := bufio.NewReader(os.Stdin)
	if _, err := in.ReadString('\n'); err != nil {
		return 0
	}
	descriptor := progressDescriptor()
	hello := func(d plugin.Descriptor) {
		data, _ := json.Marshal(d)
		fmt.Printf(`{"apiVersion":"ext.plugin/v1","id":"hello","payload":%s}`+"\n", data)
	}
	switch mode {
	case "stage-silent":
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	case "stage-garbage":
		fmt.Print("this is not json\n")
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "stage-wrong-revision":
		descriptor.Identity.Revision = "other"
		hello(descriptor)
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "stage-other-contract":
		descriptor.Contracts = descriptor.Contracts[:1]
		descriptor.Contracts[0].Operations = descriptor.Contracts[0].Operations[:1]
		hello(descriptor)
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "stage-error-hello":
		fmt.Print(`{"apiVersion":"ext.plugin/v1","id":"hello","error":{"code":"busy","message":"later"}}` + "\n")
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "stage-wrong-id":
		data, _ := json.Marshal(descriptor)
		fmt.Printf(`{"apiVersion":"ext.plugin/v1","id":"other","payload":%s}`+"\n", data)
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "stage-exit-on-call":
		hello(descriptor)
		_, _ = in.ReadString('\n')
		return 4
	default:
		return 2
	}
	return 0
}
