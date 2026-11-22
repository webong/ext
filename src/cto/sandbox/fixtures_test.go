package sandbox

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// testdata returns EXT_SANDBOX_TESTDATA when set, else this module's testdata.
func testdata() string {
	if dir := os.Getenv("EXT_SANDBOX_TESTDATA"); dir != "" {
		return dir
	}
	return "testdata"
}

func strict(t *testing.T, raw json.RawMessage, out any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
}

// same compares got with the fixture's want after decoding want into got's type, so field order
// and omitted empty fields do not matter but list order does.
func same(t *testing.T, what string, got any, want json.RawMessage) {
	t.Helper()
	decoded := reflect.New(reflect.TypeOf(got))
	strict(t, want, decoded.Interface())
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(decoded.Elem().Interface())
	if !bytes.Equal(g, w) {
		t.Errorf("%s:\n got %s\nwant %s", what, g, w)
	}
}

func sameRefused(t *testing.T, got []Refusal, want json.RawMessage) {
	t.Helper()
	var expected []Refusal
	strict(t, want, &expected)
	if len(got) == 0 && len(expected) == 0 {
		return
	}
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(expected)
	if !bytes.Equal(g, w) {
		t.Errorf("refused:\n got %s\nwant %s", g, w)
	}
}

func checkCode(t *testing.T, err error, want *string) {
	t.Helper()
	got := Code(err)
	if err != nil && got == "" {
		t.Fatalf("error %v has no code", err)
	}
	expected := ""
	if want != nil {
		expected = *want
	}
	if got != expected {
		t.Errorf("code %q, want %q (err %v)", got, expected, err)
	}
}

type fixtureFile struct {
	Version      int                `json:"version"`
	About        string             `json:"about"`
	Limits       map[string]int     `json:"limits"`
	Boundaries   []Boundary         `json:"boundaries"`
	Classes      []string           `json:"classes"`
	Reasons      []string           `json:"reasons"`
	Accept       []acceptCase       `json:"accept"`
	Validate     []validateCase     `json:"validate"`
	Grant        []grantCase        `json:"grant"`
	Required     []requiredCase     `json:"required"`
	Capabilities []capabilitiesCase `json:"capabilities"`
	Enforce      []enforceCase      `json:"enforce"`
	Report       []reportCase       `json:"report"`
	Sandboxed    []sandboxedCase    `json:"sandboxed"`
	Plan         []planCase         `json:"plan"`
}

type acceptCase struct {
	Name     string     `json:"name"`
	Accepted []Boundary `json:"accepted"`
	Boundary Boundary   `json:"boundary"`
	Code     *string    `json:"code"`
}

type validateCase struct {
	Name   string          `json:"name"`
	Policy json.RawMessage `json:"policy"`
	Code   *string         `json:"code"`
}

type grantCase struct {
	Name    string          `json:"name"`
	Request json.RawMessage `json:"request"`
	Policy  json.RawMessage `json:"policy"`
	Grant   json.RawMessage `json:"grant"`
	Refused json.RawMessage `json:"refused"`
	Code    *string         `json:"code"`
}

type requiredCase struct {
	Name    string          `json:"name"`
	Grant   json.RawMessage `json:"grant"`
	Classes []string        `json:"classes"`
	Code    *string         `json:"code"`
}

type capabilitiesCase struct {
	Name         string          `json:"name"`
	Capabilities json.RawMessage `json:"capabilities"`
	Code         *string         `json:"code"`
}

type enforceCase struct {
	Name         string          `json:"name"`
	Grant        json.RawMessage `json:"grant"`
	Capabilities json.RawMessage `json:"capabilities"`
	Accept       []string        `json:"accept"`
	Outcome      json.RawMessage `json:"outcome"`
	Code         *string         `json:"code"`
}

type reportCase struct {
	Name   string          `json:"name"`
	Report json.RawMessage `json:"report"`
	Code   *string         `json:"code"`
}

type sandboxedCase struct {
	Name      string          `json:"name"`
	Report    json.RawMessage `json:"report"`
	Sandboxed bool            `json:"sandboxed"`
}

type planCase struct {
	Name             string          `json:"name"`
	Request          json.RawMessage `json:"request"`
	Policy           json.RawMessage `json:"policy"`
	Accepted         []Boundary      `json:"accepted"`
	Capabilities     json.RawMessage `json:"capabilities"`
	AcceptUnenforced []string        `json:"acceptUnenforced"`
	Report           json.RawMessage `json:"report"`
	Code             *string         `json:"code"`
}

func loadFixtures(t *testing.T) fixtureFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdata(), "v1", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file fixtureFile
	strict(t, data, &file)
	if file.Version != 1 {
		t.Fatalf("version %d", file.Version)
	}
	return file
}

func TestFixtureVocabulary(t *testing.T) {
	file := loadFixtures(t)
	if !reflect.DeepEqual(file.Boundaries, Boundaries) {
		t.Errorf("boundaries %v, want %v", file.Boundaries, Boundaries)
	}
	if !reflect.DeepEqual(file.Classes, Classes) {
		t.Errorf("classes %v, want %v", file.Classes, Classes)
	}
	if want := []string{ReasonUnsupported, ReasonPermission, ReasonToolMissing}; !reflect.DeepEqual(file.Reasons, want) {
		t.Errorf("reasons %v, want %v", file.Reasons, want)
	}
	limits := map[string]int{
		"maxPaths": MaxPaths, "maxPathBytes": MaxPathBytes, "maxPorts": MaxPorts, "maxHosts": MaxHosts,
		"maxHostBytes": MaxHostBytes, "maxDevices": MaxDevices, "maxDeviceBytes": MaxDeviceBytes,
		"maxEnvNames": MaxEnvNames, "maxEnvBytes": MaxEnvBytes, "maxNameBytes": MaxNameBytes, "maxRefBytes": MaxRefBytes,
	}
	if !reflect.DeepEqual(file.Limits, limits) {
		t.Errorf("limits %v, want %v", file.Limits, limits)
	}
}

func TestFixtureCodesAreListed(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(testdata(), "codes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var codes struct {
		Codes []string `json:"codes"`
	}
	if err := json.Unmarshal(data, &codes); err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, c := range codes.Codes {
		listed[c] = true
	}
	file := loadFixtures(t)
	var used []*string
	for _, c := range file.Accept {
		used = append(used, c.Code)
	}
	for _, c := range file.Validate {
		used = append(used, c.Code)
	}
	for _, c := range file.Grant {
		used = append(used, c.Code)
	}
	for _, c := range file.Required {
		used = append(used, c.Code)
	}
	for _, c := range file.Capabilities {
		used = append(used, c.Code)
	}
	for _, c := range file.Enforce {
		used = append(used, c.Code)
	}
	for _, c := range file.Report {
		used = append(used, c.Code)
	}
	for _, c := range file.Plan {
		used = append(used, c.Code)
	}
	for _, code := range used {
		if code != nil && !listed[*code] {
			t.Errorf("code %q is not in codes.json", *code)
		}
	}
}

func TestFixtureAccept(t *testing.T) {
	for _, c := range loadFixtures(t).Accept {
		t.Run(c.Name, func(t *testing.T) { checkCode(t, Accept(c.Accepted, c.Boundary), c.Code) })
	}
}

func TestFixtureValidate(t *testing.T) {
	for _, c := range loadFixtures(t).Validate {
		t.Run(c.Name, func(t *testing.T) {
			var p Policy
			strict(t, c.Policy, &p)
			checkCode(t, p.Validate(), c.Code)
		})
	}
}

func TestFixtureGrant(t *testing.T) {
	for _, c := range loadFixtures(t).Grant {
		t.Run(c.Name, func(t *testing.T) {
			var request, policy Policy
			strict(t, c.Request, &request)
			strict(t, c.Policy, &policy)
			grant, refused, err := Grant(request, policy)
			checkCode(t, err, c.Code)
			if c.Code != nil {
				return
			}
			same(t, "grant", grant, c.Grant)
			sameRefused(t, refused, c.Refused)
			if grant.Validate() != nil {
				t.Error("the grant does not validate")
			}
		})
	}
}

func TestFixtureRequired(t *testing.T) {
	for _, c := range loadFixtures(t).Required {
		t.Run(c.Name, func(t *testing.T) {
			var grant Policy
			strict(t, c.Grant, &grant)
			classes, err := Required(grant)
			checkCode(t, err, c.Code)
			if c.Code == nil && !reflect.DeepEqual(classes, c.Classes) {
				t.Errorf("classes %v, want %v", classes, c.Classes)
			}
		})
	}
}

func TestFixtureCapabilities(t *testing.T) {
	for _, c := range loadFixtures(t).Capabilities {
		t.Run(c.Name, func(t *testing.T) {
			var caps Capabilities
			strict(t, c.Capabilities, &caps)
			checkCode(t, caps.Validate(), c.Code)
		})
	}
}

func TestFixtureEnforce(t *testing.T) {
	for _, c := range loadFixtures(t).Enforce {
		t.Run(c.Name, func(t *testing.T) {
			var grant Policy
			var caps Capabilities
			strict(t, c.Grant, &grant)
			strict(t, c.Capabilities, &caps)
			outcome, err := Enforce(grant, caps, c.Accept)
			checkCode(t, err, c.Code)
			if c.Outcome != nil {
				same(t, "outcome", outcome, c.Outcome)
			}
		})
	}
}

func TestFixtureReport(t *testing.T) {
	for _, c := range loadFixtures(t).Report {
		t.Run(c.Name, func(t *testing.T) {
			var r Report
			strict(t, c.Report, &r)
			checkCode(t, r.Validate(), c.Code)
		})
	}
}

func TestFixtureSandboxed(t *testing.T) {
	for _, c := range loadFixtures(t).Sandboxed {
		t.Run(c.Name, func(t *testing.T) {
			var r Report
			strict(t, c.Report, &r)
			if got := Sandboxed(r); got != c.Sandboxed {
				t.Errorf("sandboxed %v, want %v", got, c.Sandboxed)
			}
		})
	}
}

func TestFixturePlan(t *testing.T) {
	for _, c := range loadFixtures(t).Plan {
		t.Run(c.Name, func(t *testing.T) {
			var request, policy Policy
			var caps Capabilities
			strict(t, c.Request, &request)
			strict(t, c.Policy, &policy)
			strict(t, c.Capabilities, &caps)
			report, err := Plan(request, policy, c.Accepted, caps, c.AcceptUnenforced)
			checkCode(t, err, c.Code)
			if c.Report != nil {
				same(t, "report", report, c.Report)
			}
		})
	}
}
