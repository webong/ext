package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const word42 = "0x000000000000000000000000000000000000000000000000000000000000002a"

func invoke(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

func parse(t *testing.T, stdout string) result {
	t.Helper()
	var res result
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("not a result: %q: %v", stdout, err)
	}
	return res
}

func word(n uint64) string {
	const hexDigits = "0123456789abcdef"
	var b [64]byte
	for i := range b {
		b[i] = '0'
	}
	for i := 63; n > 0; i-- {
		b[i] = hexDigits[n&15]
		n >>= 4
	}
	return "0x" + string(b[:])
}

// Programs, as hex. Each returns one 32-byte word unless noted.
const (
	returns42    = "602a60005260206000f3"                              // PUSH1 42, MSTORE 0, RETURN 0..32
	push0        = "5f5f5260205ff3"                                    // PUSH0 (Shanghai): returns 0
	mcopy        = "602a600052" + "602060006020" + "5e" + "60206020f3" // MCOPY (Cancun): copies 42 to offset 32
	reverts      = "60006000fd"                                        // REVERT with no data
	spin         = "5b600056"                                          // JUMPDEST, PUSH1 0, JUMP: loops forever
	echoCalldata = "600035600052" + "60206000f3"                       // returns the first calldata word
	selfBalance  = "4760005260206000f3"                                // SELFBALANCE
	timestamp    = "4260005260206000f3"                                // TIMESTAMP
	blockNumber  = "4360005260206000f3"                                // NUMBER
	chainID      = "4660005260206000f3"                                // CHAINID
)

func TestDiscovery(t *testing.T) {
	if code, stdout, _ := invoke(t, "list"); code != 0 || stdout != "paris\nshanghai\ncancun\nprague\n" {
		t.Fatalf("list: %d %q", code, stdout)
	}
	code, stdout, _ := invoke(t, "observe")
	if code != 0 || !strings.Contains(stdout, `"selection":"cancun"`) || !strings.Contains(stdout, "EIP-1153") || !strings.Contains(stdout, "go-ethereum") ||
		!strings.Contains(stdout, `"kind":"evm"`) || !strings.Contains(stdout, `"isolation":"sandboxed"`) || !strings.Contains(stdout, `"version":"v`) {
		t.Fatalf("observe: %d %q", code, stdout)
	}
	if code, _, _ := invoke(t, "validate", "cancun"); code != 0 {
		t.Fatalf("validate: %d", code)
	}
	if code, _, stderr := invoke(t, "validate", "frontier"); code != 1 || !strings.Contains(stderr, "not available") || !strings.Contains(stderr, "prague") {
		t.Fatalf("validate unknown fork: %d %q", code, stderr)
	}
	if code, stdout, stderr := invoke(t, "doctor", ""); code != 0 || !strings.Contains(stdout, "ready (fork prague)") {
		t.Fatalf("doctor: %d %q %q", code, stdout, stderr)
	}
}

func TestRunsBytecodeWithNoChain(t *testing.T) {
	code, stdout, stderr := invoke(t, "run", "--", returns42)
	res := parse(t, stdout)
	if code != 0 || res.Status != "success" || res.ReturnData != word42 || res.Fork != "prague" || res.GasUsed == 0 {
		t.Fatalf("%d %+v %q", code, res, stderr)
	}
	// 0x prefix, whitespace and a file are all accepted.
	file := filepath.Join(t.TempDir(), "code.hex")
	if err := os.WriteFile(file, []byte("0x"+returns42+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stdout, _ := invoke(t, "run", "--", "@"+file); code != 0 || parse(t, stdout).ReturnData != word42 {
		t.Fatalf("file input: %d %q", code, stdout)
	}
}

func TestForkSelectionChangesWhichOpcodesExist(t *testing.T) {
	cases := []struct {
		fork, program string
		ok            bool
	}{
		{"paris", push0, false}, {"shanghai", push0, true}, {"cancun", push0, true},
		{"shanghai", mcopy, false}, {"cancun", mcopy, true}, {"prague", mcopy, true},
	}
	for _, c := range cases {
		code, stdout, _ := invoke(t, "run", "--", "--fork", c.fork, c.program)
		res := parse(t, stdout)
		if c.ok != (res.Status == "success") || c.ok != (code == 0) {
			t.Errorf("%s: %+v (exit %d), want ok=%v", c.fork, res, code, c.ok)
		}
		if c.ok && c.program == mcopy && res.ReturnData != word42 {
			t.Errorf("%s: MCOPY returned %s", c.fork, res.ReturnData)
		}
		if !c.ok && !strings.Contains(res.Error, "invalid opcode") {
			t.Errorf("%s: error %q", c.fork, res.Error)
		}
	}
	// The project's selection decides when no flag does, and the flag overrides it.
	if code, _, _ := invoke(t, "run", "shanghai", "--", mcopy); code != 1 {
		t.Fatalf("selection shanghai must lack MCOPY: %d", code)
	}
	if code, _, _ := invoke(t, "run", "shanghai", "--", "--fork", "cancun", mcopy); code != 0 {
		t.Fatalf("--fork overrides the selection: %d", code)
	}
}

func TestRevertAndOutOfGasAreResultsNotCrashes(t *testing.T) {
	code, stdout, _ := invoke(t, "run", "--", reverts)
	if res := parse(t, stdout); code != 1 || res.Status != "revert" {
		t.Fatalf("revert: %d %+v", code, res)
	}
	code, stdout, _ = invoke(t, "run", "--", "--gas", "5000", spin)
	if res := parse(t, stdout); code != 1 || res.Status != "error" || !strings.Contains(res.Error, "out of gas") || res.GasUsed != 5000 {
		t.Fatalf("out of gas: %d %+v", code, res)
	}
}

func TestTimeoutStopsAnUnboundedLoop(t *testing.T) {
	started := testingNow()
	code, stdout, _ := invoke(t, "run", "--", "--gas", "100000000000000", "--timeout", "200ms", spin)
	if res := parse(t, stdout); code != 124 || res.Status != "timeout" {
		t.Fatalf("timeout: %d %+v", code, res)
	}
	if elapsed := testingNow() - started; elapsed > 10_000 {
		t.Fatalf("the loop ran for %dms", elapsed)
	}
}

func TestCalldataValueAndBlockContext(t *testing.T) {
	calldata := word(0xabcdef)
	code, stdout, _ := invoke(t, "run", "--", "--input", calldata, echoCalldata)
	if res := parse(t, stdout); code != 0 || res.ReturnData != calldata {
		t.Fatalf("calldata: %d %+v", code, res)
	}
	if _, stdout, _ := invoke(t, "run", "--", "--value", "7", selfBalance); parse(t, stdout).ReturnData != word(7) {
		t.Fatalf("value: %q", stdout)
	}
	// The block context is fixed, so a run is repeatable, and each part can be set.
	if _, stdout, _ := invoke(t, "run", "--", timestamp); parse(t, stdout).ReturnData != word(1_700_000_000) {
		t.Fatalf("default timestamp: %q", stdout)
	}
	if _, stdout, _ := invoke(t, "run", "--", "--timestamp", "99", timestamp); parse(t, stdout).ReturnData != word(99) {
		t.Fatalf("timestamp flag: %q", stdout)
	}
	if _, stdout, _ := invoke(t, "run", "--", "--number", "12345", blockNumber); parse(t, stdout).ReturnData != word(12345) {
		t.Fatalf("number flag: %q", stdout)
	}
	if _, stdout, _ := invoke(t, "run", "--", "--chain-id", "5", chainID); parse(t, stdout).ReturnData != word(5) {
		t.Fatalf("chain id flag: %q", stdout)
	}
	_, first, _ := invoke(t, "run", "--", returns42)
	_, second, _ := invoke(t, "run", "--", returns42)
	if first != second {
		t.Fatalf("two identical runs differ:\n%s\n%s", first, second)
	}
}

func TestDeployThenCallInOneInvocation(t *testing.T) {
	// Creation code that returns echoCalldata as the deployed runtime code.
	runtimeCode := echoCalldata
	creation := "600b80600b600039" + "6000f3" + runtimeCode
	calldata := word(0x1234)
	code, stdout, stderr := invoke(t, "run", "--", "--deploy", "--input", calldata, creation)
	res := parse(t, stdout)
	if code != 0 || res.Status != "success" || res.ReturnData != calldata || res.Deployed == nil || res.Deployed.Size != len(runtimeCode)/2 {
		t.Fatalf("%d %+v %q", code, res, stderr)
	}
	if res.Deployed.Gas == 0 || res.GasUsed <= res.Deployed.Gas {
		t.Fatalf("gas must cover the deployment and the call: %+v", res)
	}
	// Without --input the contract is only deployed.
	if _, stdout, _ := invoke(t, "run", "--", "--deploy", creation); parse(t, stdout).Deployed == nil {
		t.Fatalf("deploy only: %q", stdout)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"run", "--"}, {"run", "--", "zz"}, {"run", "--", ""}, {"run", "--", "--gas", "0", returns42},
		{"run", "--", "--value", "-1", returns42}, {"run", "--", "--origin", "nope", returns42}, {"run", "--", "--input", "xyz", returns42},
		{"run", "--", "--fork", "frontier", returns42},
	} {
		if code, stdout, stderr := invoke(t, args...); code == 0 || stdout != "" || stderr == "" {
			t.Errorf("%v: exit %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
}

func testingNow() int64 { return time.Now().UnixMilli() }
