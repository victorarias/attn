package main

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// A healthy garden gets no notice. The whole point of driving it off the count
// is that a listing with nothing stranded is exactly what it was before.
func TestStrandedTicketNoticeIsSilentWithNothingStranded(t *testing.T) {
	for name, count := range map[string]*int{
		"absent": nil,
		"zero":   protocol.Ptr(0),
	} {
		var out strings.Builder
		printStrandedTicketNotice(&out, count)
		if out.Len() != 0 {
			t.Errorf("%s stranded count printed %q, want nothing", name, out.String())
		}
	}
}

// The notice names the real count and the command that reaches the work. Both
// halves matter: a reader who does not know the retired board exists can only
// act on being handed the way in.
func TestStrandedTicketNoticeNamesTheCountAndTheWayIn(t *testing.T) {
	var out strings.Builder
	printStrandedTicketNotice(&out, protocol.Ptr(4))
	got := out.String()
	if !strings.Contains(got, "4 crashed or failed tickets") {
		t.Errorf("notice %q does not name the count", got)
	}
	if !strings.Contains(got, "attn ticket list") {
		t.Errorf("notice %q does not say how to reach them", got)
	}

	var one strings.Builder
	printStrandedTicketNotice(&one, protocol.Ptr(1))
	if !strings.Contains(one.String(), "1 crashed or failed ticket ") {
		t.Errorf("single stranded ticket reads %q", one.String())
	}
}
