// Command ctx-evm is the built-in EVM engine adapter. It executes EVM bytecode
// in-process with no blockchain, node or network: state lives in memory for one
// invocation and the block context is fixed unless a flag changes it.
//
// This binary links go-ethereum, which is licensed under the LGPL-3.0. See
// NOTICE.md next to the adapter for the terms and how to relink it.
package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/core/vm/runtime"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	"github.com/webong/ext/pkg/plugin"
)

const (
	defaultFork = "prague"
	defaultGas  = 30_000_000
	// maxCodeBytes bounds the bytecode and calldata read from flags or files.
	maxCodeBytes = 1 << 20
)

// fork is one selectable rule set. Only post-merge rule sets are offered: the
// runtime always executes with a PREVRANDAO context, so earlier forks would not
// behave like the chain did.
type fork struct{ name, introduces string }

var forks = []fork{
	{"paris", "PREVRANDAO replaces DIFFICULTY (EIP-4399)"},
	{"shanghai", "PUSH0 (EIP-3855)"},
	{"cancun", "transient storage (EIP-1153), MCOPY (EIP-5656), BLOBHASH (EIP-4844)"},
	{"prague", "BLS12-381 precompiles (EIP-2537)"},
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	request, err := plugin.ParseAdapterInvocation(args)
	if err != nil {
		fmt.Fprintf(stderr, "evm: %v\n", err)
		return 2
	}
	switch request.Operation {
	case "list":
		for _, f := range forks {
			fmt.Fprintln(stdout, f.name)
		}
	case "observe":
		return observe(stdout)
	case "validate":
		return validate(request.Selection, stderr)
	case "doctor":
		return doctor(request.Selection, stdout, stderr)
	case "run":
		if code := validate(request.Selection, stderr); code != 0 {
			return code
		}
		return execute(request.Selection, request.Arguments, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "evm: unsupported adapter operation %s\n", request.Operation)
		return 2
	}
	return 0
}

func findFork(name string) (fork, bool) {
	for _, f := range forks {
		if f.name == name {
			return f, true
		}
	}
	return fork{}, false
}

// validate accepts a known fork, or no selection, which means the newest.
func validate(selection string, stderr io.Writer) int {
	if selection == "" {
		return 0
	}
	if _, ok := findFork(selection); !ok {
		names := make([]string, len(forks))
		for i, f := range forks {
			names[i] = f.name
		}
		fmt.Fprintf(stderr, "evm: fork %q is not available (available: %s)\n", selection, strings.Join(names, ", "))
		return 1
	}
	return 0
}

func engineVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dependency := range info.Deps {
			if dependency.Path == "github.com/ethereum/go-ethereum" {
				return dependency.Version
			}
		}
	}
	return "unknown"
}

func observe(stdout io.Writer) int {
	type context struct {
		Selection  string            `json:"selection"`
		Attributes map[string]string `json:"attributes"`
	}
	contexts := make([]context, 0, len(forks))
	for _, f := range forks {
		contexts = append(contexts, context{f.name, map[string]string{
			"kind": "evm", "isolation": "sandboxed", "introduces": f.introduces, "engine": "go-ethereum", "version": engineVersion(),
			"state": "in-memory, one invocation",
		}})
	}
	data, err := json.Marshal(map[string]any{"version": 1, "contexts": contexts})
	if err != nil {
		return 1
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}

// doctor proves the engine runs: PUSH1 42, PUSH1 0, MSTORE, PUSH1 32, PUSH1 0, RETURN.
func doctor(selection string, stdout, stderr io.Writer) int {
	if code := validate(selection, stderr); code != 0 {
		return code
	}
	name := selection
	if name == "" {
		name = defaultFork
	}
	code := []byte{0x60, 0x2a, 0x60, 0x00, 0x52, 0x60, 0x20, 0x60, 0x00, 0xf3}
	res := evaluate(name, options{gas: defaultGas, origin: defaultOrigin, coinbase: defaultCoinbase,
		number: defaultNumber, timestamp: defaultTimestamp, chainID: 1, value: new(big.Int)}, code, nil, false)
	want := "0x" + strings.Repeat("00", 31) + "2a"
	if res.Status != "success" || res.ReturnData != want {
		fmt.Fprintf(stderr, "evm: the embedded engine returned %+v\n", res)
		return 1
	}
	fmt.Fprintf(stdout, "evm: embedded go-ethereum %s ready (fork %s)\n", engineVersion(), name)
	return 0
}

var (
	defaultOrigin    = common.HexToAddress("0xdeadbeef")
	defaultCoinbase  = common.HexToAddress("0xc01nba5e")
	defaultNumber    = uint64(1)
	defaultTimestamp = uint64(1_700_000_000)
)

type options struct {
	gas               uint64
	timeout           time.Duration
	origin, coinbase  common.Address
	number, timestamp uint64
	chainID           uint64
	value             *big.Int
}

type deployment struct {
	Address string `json:"address"`
	Gas     uint64 `json:"gasUsed"`
	Size    int    `json:"codeSize"`
}

// result is what a run reports on stdout. A revert or failure is still a
// result: the process exits non-zero, and the JSON says why.
type result struct {
	Status     string      `json:"status"`
	Fork       string      `json:"fork"`
	GasUsed    uint64      `json:"gasUsed"`
	ReturnData string      `json:"returnData"`
	Error      string      `json:"error,omitempty"`
	Deployed   *deployment `json:"deployed,omitempty"`
}

func chainConfig(name string, chainID uint64) *params.ChainConfig {
	zero := func() *uint64 { v := uint64(0); return &v }
	config := &params.ChainConfig{
		ChainID: new(big.Int).SetUint64(chainID), HomesteadBlock: new(big.Int), EIP150Block: new(big.Int),
		EIP155Block: new(big.Int), EIP158Block: new(big.Int), ByzantiumBlock: new(big.Int),
		ConstantinopleBlock: new(big.Int), PetersburgBlock: new(big.Int), IstanbulBlock: new(big.Int),
		MuirGlacierBlock: new(big.Int), BerlinBlock: new(big.Int), LondonBlock: new(big.Int),
		TerminalTotalDifficulty: new(big.Int),
	}
	switch name {
	case "prague":
		config.PragueTime = zero()
		fallthrough
	case "cancun":
		config.CancunTime = zero()
		fallthrough
	case "shanghai":
		config.ShanghaiTime = zero()
	}
	if name == "cancun" || name == "prague" {
		config.BlobScheduleConfig = &params.BlobScheduleConfig{Cancun: params.DefaultCancunBlobConfig, Prague: params.DefaultPragueBlobConfig}
	}
	return config
}

// evaluate runs bytecode in a fresh in-memory state. With deploy set, code is
// creation code: it is deployed first, and input (if any) calls the new
// contract in the same state.
func evaluate(name string, o options, code, input []byte, deploy bool) result {
	res := result{Fork: name, Status: "success", ReturnData: "0x"}
	database, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	if err != nil {
		return failed(res, err)
	}
	database.AddBalance(o.origin, uint256.NewInt(0).Lsh(uint256.NewInt(1), 200), tracing.BalanceChangeUnspecified)
	random := common.Hash{}
	config := &runtime.Config{
		ChainConfig: chainConfig(name, o.chainID), Difficulty: new(big.Int), Origin: o.origin, Coinbase: o.coinbase,
		BlockNumber: new(big.Int).SetUint64(o.number), Time: o.timestamp, GasLimit: o.gas, GasPrice: new(big.Int),
		Value: o.value, BaseFee: big.NewInt(params.InitialBaseFee), BlobBaseFee: big.NewInt(params.BlobTxMinBlobGasprice),
		Random: &random, State: database,
		GetHashFn: func(n uint64) common.Hash { return common.BytesToHash([]byte(new(big.Int).SetUint64(n).String())) },
	}
	environment := runtime.NewEnv(config)
	if o.timeout > 0 {
		timer := time.AfterFunc(o.timeout, environment.Cancel)
		defer timer.Stop()
	}
	rules := config.ChainConfig.Rules(environment.Context.BlockNumber, true, environment.Context.Time)
	value, overflow := uint256.FromBig(o.value)
	if overflow {
		return failed(res, errors.New("value does not fit in 256 bits"))
	}
	target := common.BytesToAddress([]byte("contract"))
	if deploy {
		database.Prepare(rules, o.origin, o.coinbase, nil, vm.ActivePrecompiles(rules), nil)
		deployed, address, left, err := environment.Create(o.origin, code, o.gas, value)
		res.GasUsed = o.gas - left
		res.Deployed = &deployment{Address: address.Hex(), Gas: res.GasUsed, Size: len(deployed)}
		if err != nil {
			res.ReturnData = "0x" + hex.EncodeToString(deployed)
			return classify(res, environment, err)
		}
		if len(input) == 0 {
			return res
		}
		target, value = address, new(uint256.Int)
	} else {
		database.CreateAccount(target)
		database.SetCode(target, code)
	}
	database.Prepare(rules, o.origin, o.coinbase, &target, vm.ActivePrecompiles(rules), nil)
	returned, left, err := environment.Call(o.origin, target, input, o.gas, value)
	res.GasUsed += o.gas - left
	res.ReturnData = "0x" + hex.EncodeToString(returned)
	return classify(res, environment, err)
}

func classify(res result, environment *vm.EVM, err error) result {
	switch {
	// An aborted interpreter returns without an error, so the cancellation flag
	// must be checked first or a stopped run would be reported as a success.
	case environment.Cancelled():
		res.Status, res.Error = "timeout", "execution was stopped by --timeout"
	case err == nil:
	case errors.Is(err, vm.ErrExecutionReverted):
		res.Status, res.Error = "revert", err.Error()
	default:
		res.Status, res.Error = "error", err.Error()
	}
	return res
}

func failed(res result, err error) result {
	res.Status, res.Error = "error", err.Error()
	return res
}

// bytesArgument reads hex from the argument itself, or from a file as @path.
func bytesArgument(value string) ([]byte, error) {
	if strings.HasPrefix(value, "@") {
		info, err := os.Stat(value[1:])
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > 2*maxCodeBytes+64 {
			return nil, fmt.Errorf("%s is not a regular file of at most %d bytes", value[1:], maxCodeBytes)
		}
		data, err := os.ReadFile(value[1:])
		if err != nil {
			return nil, err
		}
		value = string(data)
	}
	value = strings.TrimPrefix(strings.Join(strings.Fields(value), ""), "0x")
	if len(value)/2 > maxCodeBytes {
		return nil, fmt.Errorf("more than %d bytes", maxCodeBytes)
	}
	return hex.DecodeString(value)
}

func execute(selection string, arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("evm", flag.ContinueOnError)
	flags.SetOutput(stderr)
	forkFlag := flags.String("fork", "", "rule set, overriding the selection (default "+defaultFork+")")
	gas := flags.Uint64("gas", defaultGas, "gas limit")
	timeout := flags.Duration("timeout", 0, "stop execution after this long (0 means no limit)")
	input := flags.String("input", "", "calldata as hex, or @file")
	deploy := flags.Bool("deploy", false, "treat the code as creation code, deploy it, then call it with --input")
	valueFlag := flags.String("value", "0", "wei sent with the call, in decimal")
	origin := flags.String("origin", defaultOrigin.Hex(), "caller address")
	coinbase := flags.String("coinbase", defaultCoinbase.Hex(), "block coinbase address")
	number := flags.Uint64("number", defaultNumber, "block number")
	timestamp := flags.Uint64("timestamp", defaultTimestamp, "block timestamp (fixed by default, so runs are repeatable)")
	chainID := flags.Uint64("chain-id", 1, "value of the CHAINID opcode")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "evm: usage: ctx run evm [flags] BYTECODE|@file")
		return 2
	}
	name := selection
	if *forkFlag != "" {
		name = *forkFlag
	}
	if name == "" {
		name = defaultFork
	}
	if code := validate(name, stderr); code != 0 {
		return code
	}
	code, err := bytesArgument(flags.Arg(0))
	if err != nil || len(code) == 0 {
		fmt.Fprintf(stderr, "evm: bytecode must be non-empty hex or @file: %v\n", err)
		return 2
	}
	calldata, err := bytesArgument(*input)
	if err != nil {
		fmt.Fprintf(stderr, "evm: --input: %v\n", err)
		return 2
	}
	value, ok := new(big.Int).SetString(*valueFlag, 10)
	if !ok || value.Sign() < 0 {
		fmt.Fprintln(stderr, "evm: --value must be a non-negative decimal number of wei")
		return 2
	}
	if *gas == 0 {
		fmt.Fprintln(stderr, "evm: --gas must be greater than zero")
		return 2
	}
	for _, field := range []struct{ flag, value string }{{"origin", *origin}, {"coinbase", *coinbase}} {
		if !common.IsHexAddress(field.value) {
			fmt.Fprintf(stderr, "evm: --%s must be a 20-byte hex address\n", field.flag)
			return 2
		}
	}
	res := evaluate(name, options{gas: *gas, timeout: *timeout, origin: common.HexToAddress(*origin),
		coinbase: common.HexToAddress(*coinbase), number: *number, timestamp: *timestamp, chainID: *chainID, value: value},
		code, calldata, *deploy)
	data, err := json.Marshal(res)
	if err != nil {
		fmt.Fprintf(stderr, "evm: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, string(data))
	switch res.Status {
	case "success":
		return 0
	case "timeout":
		return 124
	}
	return 1
}
