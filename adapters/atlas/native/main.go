package main

import (
	chromium "github.com/webong/ext/adapters/chromium/engine"
	"os"
)

func main() {
	config := chromium.Config{
		Name: "atlas", MacUserData: "com.openai.atlas.web", WindowsUserData: ``, WindowsRoaming: false, LinuxUserData: "",
		KeychainService: "ChatGPT Safe Storage", KeychainAccount: "ChatGPT", SecretApplication: "atlas",
		WalletFolder: "ChatGPT Atlas Keys", WalletKey: "ChatGPT Atlas Safe Storage",
	}
	os.Exit(chromium.Run(config, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
