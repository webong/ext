package main

import (
	chromium "github.com/webong/ext/adapters/chromium/engine"
	"os"
)

func main() {
	config := chromium.Config{
		Name: "helium", MacUserData: "net.imput.helium", WindowsUserData: ``, WindowsRoaming: false, LinuxUserData: "",
		KeychainService: "Helium Storage Key", KeychainAccount: "Helium", SecretApplication: "helium",
		WalletFolder: "Helium Keys", WalletKey: "Helium Safe Storage",
	}
	os.Exit(chromium.Run(config, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
