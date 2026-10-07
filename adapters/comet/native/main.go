package main

import (
	chromium "github.com/webong/ext/adapters/chromium/engine"
	"os"
)

func main() {
	config := chromium.Config{
		Name: "comet", MacUserData: "Comet/User Data", WindowsUserData: `Perplexity\Comet\User Data`, WindowsRoaming: false, LinuxUserData: "",
		KeychainService: "Comet Safe Storage", KeychainAccount: "Comet", SecretApplication: "comet",
		WalletFolder: "Comet Keys", WalletKey: "Comet Safe Storage",
	}
	os.Exit(chromium.Run(config, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
