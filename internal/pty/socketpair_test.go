package pty

import (
	"os"
	"syscall"
	"testing"
)

// newPollableSocketpair returns a socketpair whose os.Files participate in the
// runtime poller, so read deadlines fire and Close interrupts a blocked Read.
func newPollableSocketpair(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	for _, fd := range fds {
		if err := syscall.SetNonblock(fd, true); err != nil {
			_ = syscall.Close(fds[0])
			_ = syscall.Close(fds[1])
			t.Fatalf("set socketpair nonblocking: %v", err)
		}
	}
	left := os.NewFile(uintptr(fds[0]), "socketpair-left")
	right := os.NewFile(uintptr(fds[1]), "socketpair-right")
	t.Cleanup(func() { _ = left.Close(); _ = right.Close() })
	return left, right
}
