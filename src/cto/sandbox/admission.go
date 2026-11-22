package sandbox

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

// AdmissionOperation is the evaluator adapter operation that gates execution
// on the report from the prepared runner.
const AdmissionOperation = "run-verified"

// AdmissionApproval is the complete approval the consumer writes to the runner.
const AdmissionApproval = "run\n"

// maxAdmissionReportBytes includes the terminating newline.
const maxAdmissionReportBytes = 64 << 10

// ReadAdmissionReport reads one bounded JSON line. Use the same buffered
// reader for subsequent output from the runner. An empty stream returns io.EOF.
func ReadAdmissionReport(input *bufio.Reader) (Report, error) {
	line := make([]byte, 0, 512)
	for len(line) < maxAdmissionReportBytes {
		b, err := input.ReadByte()
		if err != nil {
			if len(line) == 0 {
				return Report{}, err
			}
			return Report{}, fmt.Errorf("%w: incomplete admission report", ErrInvalid)
		}
		line = append(line, b)
		if b == '\n' {
			return decodeAdmissionReport(line)
		}
	}
	return Report{}, fmt.Errorf("%w: admission report exceeds the size limit", ErrInvalid)
}

func decodeAdmissionReport(line []byte) (Report, error) {
	var report Report
	// Pointers distinguish an explicit grant and enforcement list from missing
	// or null fields. An explicit empty policy is a valid deny-by-default grant.
	wire := struct {
		*Report
		Grant    *Policy   `json:"grant"`
		Enforced *[]string `json:"enforced"`
	}{Report: &report}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil || wire.Grant == nil || wire.Enforced == nil {
		return Report{}, fmt.Errorf("%w: malformed admission report", ErrInvalid)
	}
	var trailing json.RawMessage
	if decoder.Decode(&trailing) != io.EOF || !uniqueReportFields(json.NewDecoder(bytes.NewReader(line)), 0) {
		return Report{}, fmt.Errorf("%w: malformed admission report", ErrInvalid)
	}
	report.Grant, report.Enforced = *wire.Grant, *wire.Enforced
	if err := report.Validate(); err != nil {
		return Report{}, fmt.Errorf("%w: malformed admission report", err)
	}
	return report, nil
}

// JSON decoding into structs otherwise accepts repeated fields and matches
// names case-insensitively. Contradictory evidence must be rejected.
func uniqueReportFields(decoder *json.Decoder, depth int) bool {
	if depth > 16 {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delim, container := token.(json.Delim)
	if !container {
		return true
	}
	var seen []string
	for decoder.More() {
		if delim == '{' {
			key, err := decoder.Token()
			name, ok := key.(string)
			if err != nil || !ok || slices.ContainsFunc(seen, func(old string) bool { return strings.EqualFold(old, name) }) {
				return false
			}
			seen = append(seen, name)
		}
		if !uniqueReportFields(decoder, depth+1) {
			return false
		}
	}
	_, err = decoder.Token()
	return err == nil
}

// AwaitAdmission sends the prepared runner's report on output and waits for an
// explicit approval on input. Only a nil result permits guest execution.
func AwaitAdmission(input io.Reader, output io.Writer, report Report) error {
	if err := report.Validate(); err != nil {
		return err
	}
	line, err := json.Marshal(report)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if len(line) > maxAdmissionReportBytes {
		return fmt.Errorf("%w: admission report exceeds the size limit", ErrInvalid)
	}
	if n, err := output.Write(line); err != nil {
		return err
	} else if n != len(line) {
		return io.ErrShortWrite
	}
	var approval [len(AdmissionApproval)]byte
	if _, err := io.ReadFull(input, approval[:]); err != nil || string(approval[:]) != AdmissionApproval {
		return fmt.Errorf("%w: execution was not admitted", ErrDenied)
	}
	return nil
}
