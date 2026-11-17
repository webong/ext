package yaegi_test

import (
	"io"
	"reflect"
)

func reflectValue(v any) reflect.Value { return reflect.ValueOf(v) }

func ioEOF() error { return io.EOF }
