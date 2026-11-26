// Command is a WASI test guest used by the RunCommand tests.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("no mode")
		return
	}
	switch os.Args[1] {
	case "echo":
		fmt.Printf("argv0=%s args=%s foo=%q home=%q\n", os.Args[0], strings.Join(os.Args[2:], ","), os.Getenv("FOO"), os.Getenv("HOME"))
	case "upper":
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			fmt.Println(strings.ToUpper(scanner.Text()))
		}
	case "exit":
		code, _ := strconv.Atoi(os.Args[2])
		fmt.Fprintln(os.Stderr, "exiting")
		os.Exit(code)
	case "read":
		data, err := os.ReadFile(os.Args[2])
		if err != nil {
			fmt.Println("error:", err)
			os.Exit(3)
		}
		fmt.Printf("read=%s", data)
	case "write":
		if err := os.WriteFile(os.Args[2], []byte(os.Args[3]), 0o644); err != nil {
			fmt.Println("error:", err)
			os.Exit(3)
		}
		fmt.Println("wrote")
	case "spin":
		for {
		}
	}
}
