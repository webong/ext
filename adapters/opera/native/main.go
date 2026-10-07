package main

import (
	chromium "github.com/webong/ext/adapters/chromium/engine"
	"os"
)

func main() {
	config := chromium.Config{
		Name: "opera", MacUserData: "com.operasoftware.Opera", WindowsUserData: `Opera Software\Opera Stable`, WindowsRoaming: true, LinuxUserData: "opera",
		KeychainService: "Opera Safe Storage", KeychainAccount: "Opera", SecretApplication: "opera",
		WalletFolder: "Opera Keys", WalletKey: "Opera Safe Storage",
	}
	os.Exit(chromium.Run(config, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
