//go:build windows

package systemgraph

import (
	"encoding/binary"
	"testing"
	"unsafe"
)

func TestProcessWindowsSocketTables(t *testing.T) {
	for _, tcp := range []bool{false, true} {
		for _, v6 := range []bool{false, true} {
			protocol, family, width, pidOffset := "udp", uint32(2), 12, 8
			if tcp {
				protocol = "tcp"
				width, pidOffset = 24, 20
			}
			if v6 {
				family = 23
				width, pidOffset = 28, 24
				if tcp {
					width, pidOffset = 56, 52
				}
			}
			table := make([]byte, 4+width)
			binary.LittleEndian.PutUint32(table, 1)
			row := table[4:]
			binary.LittleEndian.PutUint32(row[pidOffset:], 123)
			if v6 {
				row[15] = 1
				binary.BigEndian.PutUint16(row[20:], 8080)
				if tcp {
					binary.LittleEndian.PutUint32(row[48:], 2)
				}
			} else {
				offset := 0
				if tcp {
					offset = 4
					binary.LittleEndian.PutUint32(row, 2)
				}
				copy(row[offset:], []byte{127, 0, 0, 1})
				binary.BigEndian.PutUint16(row[offset+4:], 8080)
			}
			p := newProcessInfo(123)
			parseWindowsSockets(&p, table, protocol, family, 10)
			want := "127.0.0.1:8080"
			if v6 {
				want = "[::1]:8080"
			}
			if len(p.Resources) != 1 || p.Resources[0].LocalAddress != want {
				t.Fatalf("tcp=%v v6=%v: %+v", tcp, v6, p)
			}
			if tcp && p.Resources[0].State != "LISTEN" {
				t.Fatal(p.Resources[0])
			}
			other := newProcessInfo(124)
			parseWindowsSockets(&other, table, protocol, family, 10)
			if len(other.Resources) != 0 {
				t.Fatal("foreign process sockets leaked")
			}
			parseWindowsSockets(&p, table[:len(table)-1], protocol, family, 10)
			if p.Coverage["socket"].State != "partial" {
				t.Fatal("malformed table accepted")
			}
		}
	}
}
func TestProcessWindowsSnapshotABI(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("64-bit ABI assertions")
	}
	entry := processHandleEntry{}
	va := processVAEntry{}
	if unsafe.Offsetof(entry.TypeName) != 64 || unsafe.Offsetof(entry.Name) != 80 || unsafe.Sizeof(entry) < 136 {
		t.Fatalf("handle ABI: type=%d name=%d size=%d", unsafe.Offsetof(entry.TypeName), unsafe.Offsetof(entry.Name), unsafe.Sizeof(entry))
	}
	if unsafe.Offsetof(va.Name) != 72 || unsafe.Sizeof(va) != 80 {
		t.Fatalf("mapping ABI: name=%d size=%d", unsafe.Offsetof(va.Name), unsafe.Sizeof(va))
	}
}
func restrictProcessFixture() error { return nil }
