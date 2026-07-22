//go:build darwin && arm64

package ghosttyvt

/*
#include <stddef.h>
#include <stdint.h>
#include <ghostty/vt.h>
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"
)

// goWritePty is invoked synchronously by libghostty-vt during vt_write when the
// terminal needs to write a query response back to the pty. userdata carries a
// cgo.Handle VALUE (installed as an opaque void*, never a Go pointer) that
// references the owning terminal's respSink. The callback runs on the same
// goroutine that called Write — which holds the Terminal mutex — but appends
// under the sink's own lock so a concurrent DrainResponses stays race-free.
//
// The callback MUST NOT call back into vt_write on the same terminal.
//
//export goWritePty
func goWritePty(term C.GhosttyTerminal, userdata unsafe.Pointer, data *C.uint8_t, length C.size_t) {
	if userdata == nil || length == 0 {
		return
	}
	s, ok := cgo.Handle(uintptr(userdata)).Value().(*respSink)
	if !ok {
		return
	}
	s.mu.Lock()
	s.buf = append(s.buf, C.GoBytes(unsafe.Pointer(data), C.int(length))...)
	s.mu.Unlock()
}
