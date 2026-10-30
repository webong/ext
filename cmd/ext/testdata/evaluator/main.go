// A portable evaluator fixture for the cross-product adapter acceptance test.
package main

import (
	"fmt"
	"os"
)

func main() {
	switch os.Args[1] {
	case "observe":
		fmt.Println(`{"version":1,"contexts":[{"selection":"embedded","attributes":{"kind":"wasm","isolation":"sandboxed"}}]}`)
	case "doctor":
		fmt.Println("evaluator doctor")
		if os.Getenv("EXT_TEST_DOCTOR_FAIL") == "1" {
			os.Exit(7)
		}
	case "run":
		fmt.Println("evaluated")
	}
}
