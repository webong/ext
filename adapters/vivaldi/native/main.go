package main

import (
	chromium "github.com/webong/ext/adapters/chromium/engine"
	"os"
)

func main() {
	config := chromium.Config{
		Name: "vivaldi", MacUserData: "Vivaldi", WindowsUserData: `Vivaldi\User Data`, WindowsRoaming: false, LinuxUserData: "vivaldi",
		KeychainService: "Vivaldi Safe Storage", KeychainAccount: "Vivaldi", SecretApplication: "vivaldi",
		WalletFolder: "Vivaldi Keys", WalletKey: "Vivaldi Safe Storage",
	}
	os.Exit(chromium.Run(config, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
