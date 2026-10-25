"""Wire an independent Zig SDK stdio host directly to a bridge process."""
import subprocess
import sys
host, bridge = sys.argv[1:]
a = subprocess.Popen([host, '--stdio'], stdin=subprocess.PIPE, stdout=subprocess.PIPE)
b = None
try:
    b = subprocess.Popen([bridge], stdin=a.stdout, stdout=a.stdin, env={})
    a.stdout.close()
    a.stdin.close()
    assert a.wait(timeout=10) == 0, 'Zig SDK host failed'
    # EOF is a clean disconnect.
    assert b.wait(timeout=10) == 0, 'bridge did not stop'
finally:
    for child in (a, b):
        if child and child.poll() is None:
            child.kill()
            child.wait()
print('Independent Zig SDK host passed')
