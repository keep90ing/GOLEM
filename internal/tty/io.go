package tty

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// CopyInputUntilClosed copies data from stdin to dst until stdin is closed or the done channel is closed.
func CopyInputUntilClosed(dst io.Writer, stdin *os.File, done <-chan struct{}) {
	fd := int(stdin.Fd())
	buf := make([]byte, 32*1024)

	for {
		select {
		case <-done:
			return
		default:
		}

		pollFds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if _, err := unix.Poll(pollFds, 200); err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			fmt.Fprintf(os.Stderr, "something went wrong while polling stdin: %v\n", err)
			return
		}

		revents := pollFds[0].Revents
		if revents&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0 {
			return
		}

		if revents&unix.POLLIN == 0 {
			continue
		}

		n, err := unix.Read(fd, buf)
		if n > 0 {
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				if !IsBrokenPipe(writeErr) {
					fmt.Fprintf(os.Stderr, "something went wrong while writing container input: %v\n", writeErr)
				}
				return
			}
		}

		if err != nil {
			switch {
			case errors.Is(err, unix.EINTR), errors.Is(err, unix.EAGAIN):
				continue
			case errors.Is(err, io.EOF):
				return
			default:
				fmt.Fprintf(os.Stderr, "read stdin failed: %v\n", err)
				return
			}
		}
	}
}

// IsBrokenPipe reports whether err indicates that the other side of a pipe has been closed.
func IsBrokenPipe(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) || errors.Is(err, unix.EPIPE) {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return IsBrokenPipe(opErr.Err)
	}

	return false
}