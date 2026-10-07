package main

import (
	chromium "github.com/webong/ext/adapters/chromium/engine"
	"os"
)

func main() {
	config := chromium.Config{
		Name: "arc", MacUserData: "Arc/User Data", WindowsUserData: `Packages\TheBrowserCompany.Arc_ttt1ap7aakyb4\LocalCache\Local\Arc\User Data`, WindowsRoaming: false, LinuxUserData: "",
		KeychainService: "Arc Safe Storage", KeychainAccount: "Arc", SecretApplication: "arc",
		WalletFolder: "Arc Keys", WalletKey: "Arc Safe Storage",
	}
	os.Exit(chromium.Run(config, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
