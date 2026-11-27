package nativeplugin

import "golang.org/x/sys/unix"

func socketPeerUID(fd int) (uint32, error) {
	credentials, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return 0, err
	}
	return credentials.Uid, nil
}
