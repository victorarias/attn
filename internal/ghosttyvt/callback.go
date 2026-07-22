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
// terminal needs to write a query response back to the pty. userdata points at
// the owning Terminal's cgo.Handle field. The callback runs on the same
// goroutine that called Write, which holds t.mu, so appending is race-free.
//
// The callback MUST NOT call back into vt_write on the same terminal.
//
//export goWritePty
func goWritePty(term C.GhosttyTerminal, userdata unsafe.Pointer, data *C.uint8_t, length C.size_t) {
	if userdata == nil || length == 0 {
		return
	}
	h := *(*cgo.Handle)(userdata)
	t, ok := h.Value().(*Terminal)
	if !ok {
		return
	}
	t.respBuf = append(t.respBuf, C.GoBytes(unsafe.Pointer(data), C.int(length))...)
}
