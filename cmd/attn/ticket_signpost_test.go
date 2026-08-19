package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// A signpost is only useful if it names the verb the caller reached for and the
// garden command that replaced it. An entry missing either is a dead end for the
// agent that hit it.
func TestTicketSignpostsNameTheVerbAndItsGardenReplacement(t *testing.T) {
	for _, verb := range ticketSignpostVerbs() {
		var out bytes.Buffer
		fprintTicketSignpost(&out, verb)
		text := out.String()
		if !strings.Contains(text, "attn ticket "+verb) {
			t.Errorf("signpost for %q does not name the verb: %q", verb, text)
		}
		if !strings.Contains(text, "attn seed ") {
			t.Errorf("signpost for %q names no garden command: %q", verb, text)
		}
		if !strings.Contains(text, "attn ticket show") || !strings.Contains(text, "attn ticket list") {
			t.Errorf("signpost for %q does not say the read verbs still work: %q", verb, text)
		}
	}
}

// The router and the signpost table have to agree: a write verb the router sends
// here with no entry would print a bare fallback, and a table entry the router
// never routes is a signpost nobody can reach. Both halves are read from the
// source so a verb added or moved later cannot go unnoticed.
func TestEveryTicketWriteVerbSignposts(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	routed := ticketRouterSignpostCases(t, string(source))
	if len(routed) == 0 {
		t.Fatal("found no signposted verbs in runTicket's router")
	}
	for _, verb := range routed {
		if _, ok := ticketSignposts[verb]; !ok {
			t.Errorf("runTicket signposts %q but ticketSignposts has no entry for it", verb)
		}
	}
	for verb := range ticketSignposts {
		if !contains(routed, verb) {
			t.Errorf("ticketSignposts covers %q but runTicket never routes it to a signpost", verb)
		}
	}
	// The two read verbs must not be in the table: a done ticket has no garden
	// equivalent, so they keep serving the archived store forever.
	for _, read := range []string{"list", "show"} {
		if _, ok := ticketSignposts[read]; ok {
			t.Errorf("%q is a read verb and must not signpost", read)
		}
	}
}

// ticketRouterSignpostCases reads the verbs from the router's own
// signpostTicketVerb case, so the test is driven by the live switch rather than
// a second hand-kept list.
func ticketRouterSignpostCases(t *testing.T, source string) []string {
	t.Helper()
	body, ok := functionBody(source, "func runTicket() {")
	if !ok {
		t.Fatal("could not find runTicket in main.go")
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "signpostTicketVerb(") {
			continue
		}
		for j := i; j >= 0; j-- {
			trimmed := strings.TrimSpace(lines[j])
			if !strings.HasPrefix(trimmed, "case ") {
				continue
			}
			verbs := []string{}
			for _, quoted := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(trimmed, "case "), ":"), ", ") {
				verbs = append(verbs, strings.Trim(strings.TrimSpace(quoted), `"`))
			}
			return verbs
		}
	}
	t.Fatal("runTicket routes nothing to signpostTicketVerb")
	return nil
}

// functionBody returns the source between a function's opening line and the
// closing brace in column zero that ends it.
func functionBody(source, signature string) (string, bool) {
	start := strings.Index(source, signature)
	if start < 0 {
		return "", false
	}
	rest := source[start+len(signature):]
	end := strings.Index(rest, "\n}")
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

// The help an agent reads before running anything has to say the same thing the
// signposts do, or it teaches the retired surface one more time.
func TestTicketHelpNamesTheReadVerbsAndTheRetiredOnes(t *testing.T) {
	var out bytes.Buffer
	writeTicketHelp(&out)
	help := out.String()
	if !strings.Contains(help, "retired") {
		t.Errorf("ticket help does not say tickets retired: %q", help)
	}
	for _, verb := range ticketSignpostVerbs() {
		if !strings.Contains(help, verb) {
			t.Errorf("ticket help does not name the retired verb %q: %q", verb, help)
		}
	}
	if !strings.Contains(help, "list") || !strings.Contains(help, "show") {
		t.Errorf("ticket help does not document the surviving read verbs: %q", help)
	}
}

// --ticket and --confirm are the delegation half of the retirement: a caller on
// stale guidance gets told where the capability went, not "flag provided but not
// defined" and not a delegation that silently ignored the flag.
func TestDelegateRefusesRetiredTicketFlags(t *testing.T) {
	t.Setenv("ATTN_SESSION_ID", "sess-1")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"ticket", []string{"--ticket", "some-ticket"}, "attn seed plant"},
		{"confirm", []string{"--brief", "do a thing", "--confirm"}, "attn seed tend"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseDelegateArgs(tc.args)
			if err == nil {
				t.Fatalf("parseDelegateArgs(%v) = nil error, want a signpost", tc.args)
			}
			if !strings.Contains(err.Error(), "retired") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("parseDelegateArgs(%v) error = %q, want it to name the retirement and %q", tc.args, err, tc.want)
			}
		})
	}
}
