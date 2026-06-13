package notebook

import (
	"reflect"
	"testing"
)

func TestLinks(t *testing.T) {
	body := `See [the decision](/memory/decisions/foo.md) and
[a gotcha](/memory/gotchas/bar.md#section) for context.
External [link](https://example.com/x.md) and a [relative](foo.md) one are ignored,
as is an [anchor](#top). The decision is referenced [again](/memory/decisions/foo.md).`

	got := Links(body)
	want := []string{
		"/memory/decisions/foo.md",
		"/memory/gotchas/bar.md#section",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Links = %#v, want %#v", got, want)
	}
}

func TestLinksEmpty(t *testing.T) {
	if got := Links("no links here"); len(got) != 0 {
		t.Fatalf("Links = %#v, want empty", got)
	}
}
