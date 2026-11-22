package driver_test

import (
	"errors"
	"testing"

	"github.com/webong/ext/pkg/ext-go/core/driver"
)

type fake struct {
	name      driver.Name
	available bool
	features  int
}

func (f fake) Name() driver.Name { return f.name }
func (f fake) Available() bool   { return f.available }

var errLacks = errors.New("lacks a feature")

func needs(n int) func(fake) error {
	return func(f fake) error {
		if f.features < n {
			return errLacks
		}
		return nil
	}
}

func TestChoose(t *testing.T) {
	drivers := []fake{{driver.Go, true, 1}, {driver.PureGo, false, 3}, {driver.C2Go, true, 2}, {driver.CGo, false, 3}}
	for _, c := range []struct {
		want driver.Name
		need int
		got  driver.Name
		err  error
	}{
		{driver.Auto, 0, driver.C2Go, nil},
		{"", 2, driver.C2Go, nil},
		{driver.Auto, 3, "", driver.ErrUnavailable},
		{driver.Go, 0, driver.Go, nil},
		{driver.Go, 2, "", errLacks},
		{driver.CGo, 0, "", driver.ErrUnavailable},
		{"rust", 0, "", driver.ErrUnknown},
	} {
		d, err := driver.Choose(drivers, c.want, needs(c.need))
		if c.err != nil {
			if !errors.Is(err, c.err) {
				t.Errorf("Choose(%q, %d) = %v, %v; want %v", c.want, c.need, d, err, c.err)
			}
			continue
		}
		if err != nil || d.Name() != c.got {
			t.Errorf("Choose(%q, %d) = %v, %v; want %s", c.want, c.need, d, err, c.got)
		}
	}
	if _, err := driver.Choose([]fake{{driver.Go, true, 0}}, driver.PureGo, nil); !errors.Is(err, driver.ErrUnavailable) {
		t.Errorf("a driver the library does not list: %v", err)
	}
}

func TestFromEnvironment(t *testing.T) {
	t.Setenv(driver.Environment, " purego ")
	if n, err := driver.FromEnvironment(); err != nil || n != driver.PureGo {
		t.Errorf("FromEnvironment = %q, %v", n, err)
	}
	t.Setenv(driver.Environment, "")
	if n, err := driver.FromEnvironment(); err != nil || n != driver.Auto {
		t.Errorf("unset FromEnvironment = %q, %v", n, err)
	}
}
