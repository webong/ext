package main

import (
	chromium "github.com/webong/ext/adapters/chromium/engine"
	"os"
)

func main() {
	config := chromium.Config{
		Name: "whale", MacUserData: "Naver/Whale", WindowsUserData: `Naver\Naver Whale\User Data`, WindowsRoaming: false, LinuxUserData: "naver-whale",
		KeychainService: "Whale Safe Storage", KeychainAccount: "Whale", SecretApplication: "whale",
		WalletFolder: "Whale Keys", WalletKey: "Whale Safe Storage",
	}
	os.Exit(chromium.Run(config, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
