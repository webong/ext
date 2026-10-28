//go:build linux

package system

import (
	"io/fs"
	"os"
	"syscall"
	"testing"
)

func TestStorageCapacityLinux(t *testing.T) {
	storage, err := linuxStorageCapacity(func(path string) ([]byte, error) {
		if path == "/proc/mounts" {
			return []byte("overlay / overlay rw,relatime 0 0\ntmpfs /run tmpfs rw,nosuid 0 0\n/dev/sda1 /mnt/data ext4 ro 0 0\n"), nil
		}
		return nil, fs.ErrNotExist
	}, func(path string) (*syscall.Statfs_t, error) {
		switch path {
		case "/":
			return &syscall.Statfs_t{Blocks: 1000, Bavail: 500, Frsize: 4096}, nil
		case "/run":
			return &syscall.Statfs_t{Blocks: 200, Bavail: 200, Frsize: 4096}, nil
		case "/mnt/data":
			return &syscall.Statfs_t{Blocks: 5000, Bavail: 1000, Frsize: 4096}, nil
		default:
			return nil, fs.ErrNotExist
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(storage) != 3 {
		t.Fatalf("expected 3 mounts, got %d: %#v", len(storage), storage)
	}
	if storage[0].MountPoint != "/" || storage[0].Device != "overlay" {
		t.Fatalf("first mount = %#v", storage[0])
	}
	if storage[0].TotalBytes != 1000*4096 || storage[0].AvailableBytes != 500*4096 {
		t.Fatalf("root bytes = %#v", storage[0])
	}
	if storage[0].ReadOnly {
		t.Fatalf("root should not be read-only: %#v", storage[0])
	}
	if storage[2].MountPoint != "/mnt/data" || !storage[2].ReadOnly {
		t.Fatalf("data mount should be read-only: %#v", storage[2])
	}
}

func TestStorageCapacityReadError(t *testing.T) {
	storage, err := linuxStorageCapacity(func(path string) ([]byte, error) {
		return nil, os.ErrPermission
	}, func(path string) (*syscall.Statfs_t, error) {
		return nil, os.ErrPermission
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(storage) != 0 {
		t.Fatalf("expected no mounts, got %#v", storage)
	}
}

func TestPowerCapacityLinux(t *testing.T) {
	power, err := linuxPowerCapacity(func(string) ([]os.DirEntry, error) {
		return []os.DirEntry{
			fakeDirEntry{name: "BAT0"},
			fakeDirEntry{name: "ACAD"},
		}, nil
	}, func(path string) ([]byte, error) {
		switch path {
		case "/sys/class/power_supply/BAT0/capacity":
			return []byte("75\n"), nil
		case "/sys/class/power_supply/BAT0/status":
			return []byte("discharging\n"), nil
		case "/sys/class/power_supply/ACAD/capacity":
			return []byte("100\n"), nil
		case "/sys/class/power_supply/ACAD/status":
			return []byte("charging\n"), nil
		default:
			return nil, fs.ErrNotExist
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if power == nil {
		t.Fatal("expected power info")
	}
	if power.State != PowerStateCharging {
		t.Fatalf("state = %q", power.State)
	}
	if power.Percentage != 100 {
		t.Fatalf("percentage = %d", power.Percentage)
	}
}

type fakeDirEntry struct{ name string }

func (f fakeDirEntry) Name() string      { return f.name }
func (f fakeDirEntry) IsDir() bool       { return false }
func (f fakeDirEntry) Type() os.FileMode { return 0 }
func (f fakeDirEntry) Info() (os.FileInfo, error) {
	return nil, fs.ErrNotExist
}
