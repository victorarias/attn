package pty

import "testing"

func TestGhosttyViewportTail(t *testing.T) {
	tests := []struct {
		name  string
		plain string
		rows  int
		want  string
	}{
		{
			name:  "takes last rows and trims trailing blanks",
			plain: "scroll1\nscroll2\nprompt$ \n\n",
			rows:  2,
			want:  "scroll2\nprompt$",
		},
		{
			name:  "trims trailing per-row whitespace",
			plain: "a   \nb  ",
			rows:  2,
			want:  "a\nb",
		},
		{
			name:  "fewer lines than rows returns all",
			plain: "only",
			rows:  10,
			want:  "only",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ghosttyViewportTail(tt.plain, tt.rows); got != tt.want {
				t.Errorf("ghosttyViewportTail(%q,%d) = %q, want %q", tt.plain, tt.rows, got, tt.want)
			}
		})
	}
}

func TestFirstDivergentRow(t *testing.T) {
	if _, ok := firstDivergentRow([]string{"a", "b"}, []string{"a", "b"}); ok {
		t.Errorf("identical inputs reported divergence")
	}
	row, ok := firstDivergentRow([]string{"a", "b", "c"}, []string{"a", "X", "c"})
	if !ok || row != 1 {
		t.Errorf("got (row=%d ok=%v), want (1 true)", row, ok)
	}
	// Length mismatch: the extra row diverges against "".
	row, ok = firstDivergentRow([]string{"a"}, []string{"a", "b"})
	if !ok || row != 1 {
		t.Errorf("length mismatch: got (row=%d ok=%v), want (1 true)", row, ok)
	}
}
