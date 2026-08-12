package prosegate

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Filter mode for the pipeline experiment: reads {"before","after"} JSONL from
// ATTN_STRUCT_IN, writes the real Structure.Lost verdict per pair to
// ATTN_STRUCT_OUT, so the experiment exercises the shipping tripwire itself.
func TestScratchStructCheck(t *testing.T) {
	in, out := os.Getenv("ATTN_STRUCT_IN"), os.Getenv("ATTN_STRUCT_OUT")
	if in == "" || out == "" {
		t.Skip("set ATTN_STRUCT_IN and ATTN_STRUCT_OUT")
	}
	f, err := os.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	o, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	enc := json.NewEncoder(o)
	for sc.Scan() {
		var p struct{ Before, After string }
		if err := json.Unmarshal(sc.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		lost := StructureOf(p.Before).Lost(StructureOf(p.After))
		enc.Encode(map[string]string{"lost": strings.Join(lost, ", ")})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
}
