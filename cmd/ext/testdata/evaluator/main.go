// A portable evaluator fixture for the cross-product adapter acceptance test.
package main

import (
	"bufio"
	"fmt"
	"os"
)

func main() {
	switch os.Args[1] {
	case "observe":
		fmt.Println(`{"version":1,"contexts":[{"selection":"embedded","attributes":{"kind":"wasm","isolation":"sandboxed","boundary":"runtime"}}]}`)
	case "doctor":
		fmt.Println("evaluator doctor")
		if os.Getenv("EXT_TEST_DOCTOR_FAIL") == "1" {
			os.Exit(7)
		}
	case "run":
		fmt.Println("evaluated")
	case "run-verified":
		if os.Getenv("EXT_TEST_NO_EVIDENCE") == "1" {
			return
		}
		fmt.Println(`{"boundary":"runtime","driver":"fixture","grant":{},"enforced":["filesystem","network.none","process","devices","environment","lifecycle.fresh"]}`)
		approval, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil || approval != "run\n" {
			os.Exit(126)
		}
		fmt.Println("evaluated")
	}
}
