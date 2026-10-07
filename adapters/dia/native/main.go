package main

import (
	chromium "github.com/webong/ext/adapters/chromium/engine"
	"os"
)

func main() {
	config := chromium.Config{
		Name: "dia", MacUserData: "Dia/User Data", WindowsUserData: ``, WindowsRoaming: false, LinuxUserData: "",
		KeychainService: "Dia Safe Storage", KeychainAccount: "Dia", SecretApplication: "dia",
		WalletFolder: "Dia Keys", WalletKey: "Dia Safe Storage",
	}
	os.Exit(chromium.Run(config, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
