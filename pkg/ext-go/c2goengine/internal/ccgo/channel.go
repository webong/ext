//go:build (darwin && amd64) || (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ccgo

import (
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"modernc.org/libc"
)

// The channel layer under the host (lib/plugin/src/channel.h) in Go: one child process and the byte
// stream to it. host.c owns the protocol, deadlines and state; this only spawns the child, moves
// bytes with the nonblocking contract the host expects, and kills or reaps it.

const (
	channelBufferSize = 64 << 10
)

// launch mirrors ext_launch.
type launch struct {
	path, arguments, environment, directory uintptr
	captureStderr                           int32
	_                                       int32
	stderrLimit                             uintptr
}

type goChannel struct {
	mu      sync.Mutex
	changed chan struct{} // closed and replaced on every state change
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	rbuf    []byte
	reof    bool
	wbuf    []byte
	werr    bool
	killed  bool
	wg      sync.WaitGroup
	stderr  *retainer
}

// retainer keeps the first limit bytes written to it and discards the rest.
type retainer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (r *retainer) Write(p []byte) (int, error) {
	r.mu.Lock()
	if room := r.limit - len(r.data); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		r.data = append(r.data, p[:room]...)
	}
	r.mu.Unlock()
	return len(p), nil
}

var (
	channels       sync.Map // uintptr -> *goChannel
	channelCounter atomic.Uintptr
)

// signal wakes everything that waits on the channel. mu must be held.
func (c *goChannel) signal() {
	close(c.changed)
	c.changed = make(chan struct{})
}

func cStrings(p uintptr) []string {
	var out []string
	for p != 0 {
		s := *(*uintptr)(unsafe.Pointer(p))
		if s == 0 {
			break
		}
		out = append(out, libc.GoString(s))
		p += unsafe.Sizeof(uintptr(0))
	}
	return out
}

func channelIsAbsolute(_ *libc.TLS, path uintptr) int32 {
	if path != 0 && *(*byte)(unsafe.Pointer(path)) == '/' {
		return 1
	}
	return 0
}

func channelOpen(_ *libc.TLS, l uintptr, out uintptr) int32 {
	ln := (*launch)(unsafe.Pointer(l))
	path := libc.GoString(ln.path)
	info, err := os.Stat(path)
	if err != nil {
		return stIO
	}
	if !info.Mode().IsRegular() {
		return stInvalid
	}
	c := &goChannel{changed: make(chan struct{})}
	args := cStrings(ln.arguments)
	if len(args) == 0 {
		args = []string{path}
	}
	env := cStrings(ln.environment)
	if env == nil {
		env = []string{}
	}
	c.cmd = &exec.Cmd{Path: path, Args: args, Env: env}
	if ln.directory != 0 {
		c.cmd.Dir = libc.GoString(ln.directory)
	}
	if ln.captureStderr != 0 {
		c.stderr = &retainer{limit: int(ln.stderrLimit)}
		c.cmd.Stderr = c.stderr
	} else {
		c.cmd.Stderr = os.Stderr
	}
	if c.stdin, err = c.cmd.StdinPipe(); err != nil {
		return stIO
	}
	if c.stdout, err = c.cmd.StdoutPipe(); err != nil {
		return stIO
	}
	if err = c.cmd.Start(); err != nil {
		return stIO
	}
	c.wg.Add(2)
	go c.pump()
	go c.drain()
	id := channelCounter.Add(1) << 4
	channels.Store(id, c)
	*(*uintptr)(unsafe.Pointer(out)) = id
	return stOK
}

// pump moves the child's output into the read buffer, stopping while it is full.
func (c *goChannel) pump() {
	defer c.wg.Done()
	tmp := make([]byte, 32<<10)
	for {
		c.mu.Lock()
		for len(c.rbuf) >= channelBufferSize && !c.killed {
			changed := c.changed
			c.mu.Unlock()
			<-changed
			c.mu.Lock()
		}
		if c.killed {
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
		n, err := c.stdout.Read(tmp)
		c.mu.Lock()
		c.rbuf = append(c.rbuf, tmp[:n]...)
		if err != nil {
			c.reof = true
		}
		c.signal()
		c.mu.Unlock()
		if err != nil {
			return
		}
	}
}

// drain writes what the host queued to the child's input.
func (c *goChannel) drain() {
	defer c.wg.Done()
	for {
		c.mu.Lock()
		for len(c.wbuf) == 0 && !c.killed {
			changed := c.changed
			c.mu.Unlock()
			<-changed
			c.mu.Lock()
		}
		if c.killed {
			c.mu.Unlock()
			return
		}
		data := c.wbuf
		c.wbuf = nil
		c.signal()
		c.mu.Unlock()
		if _, err := c.stdin.Write(data); err != nil {
			c.mu.Lock()
			c.werr = true
			c.signal()
			c.mu.Unlock()
			return
		}
	}
}

func channelByHandle(h uintptr) *goChannel {
	v, _ := channels.Load(h)
	c, _ := v.(*goChannel)
	return c
}

func channelWait(_ *libc.TLS, h uintptr, write int32, timeoutMs int32) int32 {
	c := channelByHandle(h)
	if c == nil {
		return stIO
	}
	var timer <-chan time.Time
	if timeoutMs > 0 {
		t := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
		defer t.Stop()
		timer = t.C
	}
	for {
		c.mu.Lock()
		var ready, hungUp bool
		if write != 0 {
			ready, hungUp = len(c.wbuf) < channelBufferSize, c.werr || c.killed
		} else {
			ready, hungUp = len(c.rbuf) > 0, c.reof || c.killed
		}
		changed := c.changed
		c.mu.Unlock()
		if write != 0 && hungUp || write == 0 && hungUp && !ready {
			return stIO
		}
		if ready {
			return stOK
		}
		if timeoutMs == 0 {
			return stTimeout
		}
		select {
		case <-changed:
		case <-timer:
			return stTimeout
		}
	}
}

func channelRead(_ *libc.TLS, h uintptr, buf uintptr, n uintptr) int64 {
	c := channelByHandle(h)
	if c == nil {
		return -1
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.killed {
		return -1
	}
	if len(c.rbuf) == 0 {
		if c.reof {
			return -1
		}
		return 0
	}
	k := copy(unsafe.Slice((*byte)(unsafe.Pointer(buf)), int(n)), c.rbuf)
	c.rbuf = c.rbuf[k:]
	c.signal()
	return int64(k)
}

func channelWrite(_ *libc.TLS, h uintptr, buf uintptr, n uintptr) int64 {
	c := channelByHandle(h)
	if c == nil {
		return -1
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.killed || c.werr {
		return -1
	}
	room := channelBufferSize - len(c.wbuf)
	if room <= 0 {
		return 0
	}
	k := int(n)
	if k > room {
		k = room
	}
	c.wbuf = append(c.wbuf, unsafe.Slice((*byte)(unsafe.Pointer(buf)), k)...)
	c.signal()
	return int64(k)
}

func (c *goChannel) kill() {
	c.mu.Lock()
	first := !c.killed
	c.killed = true
	c.signal()
	c.mu.Unlock()
	if !first {
		return
	}
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_ = c.stdin.Close()
	_ = c.stdout.Close()
}

func channelKill(_ *libc.TLS, h uintptr) {
	if c := channelByHandle(h); c != nil {
		c.kill()
	}
}

func channelDestroy(_ *libc.TLS, h uintptr) {
	c := channelByHandle(h)
	if c == nil {
		return
	}
	c.kill()
	_ = c.cmd.Wait()
	c.wg.Wait()
	channels.Delete(h)
}

func channelStderr(_ *libc.TLS, h uintptr, buf uintptr, capacity uintptr) uintptr {
	c := channelByHandle(h)
	if c == nil || c.stderr == nil {
		return 0
	}
	c.stderr.mu.Lock()
	defer c.stderr.mu.Unlock()
	n := len(c.stderr.data)
	if n > 0 && buf != 0 {
		k := n
		if k > int(capacity) {
			k = int(capacity)
		}
		copy(unsafe.Slice((*byte)(unsafe.Pointer(buf)), k), c.stderr.data)
	}
	return uintptr(n)
}

// channelOps mirrors ext_channel_ops; the function values stay referenced from here.
type channelOps struct{ isAbsolute, open, wait, read, write, kill, destroy, stderr uintptr }

var (
	fIsAbsolute = channelIsAbsolute
	fOpen       = channelOpen
	fWait       = channelWait
	fRead       = channelRead
	fWrite      = channelWrite
	fKill       = channelKill
	fDestroy    = channelDestroy
	fStderr     = channelStderr
	installed   channelOps
)

func funcPointer[F any](f *F) uintptr { return *(*uintptr)(unsafe.Pointer(f)) }

func init() {
	installed = channelOps{funcPointer(&fIsAbsolute), funcPointer(&fOpen), funcPointer(&fWait), funcPointer(&fRead), funcPointer(&fWrite), funcPointer(&fKill), funcPointer(&fDestroy), funcPointer(&fStderr)}
	ext_channel_ops_table = uintptr(unsafe.Pointer(&installed))
}
