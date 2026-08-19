package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// A closed crown over open work must never close silently: the harvest that
// strands growing children says so on the same screen that confirmed the move.
func TestFprintTransitionWarnsOnClosingACrownWithOpenChildren(t *testing.T) {
	var buf bytes.Buffer
	fprintTransition(&buf, &protocol.SeedTransitionResult{
		Seed: protocol.Seed{
			ID: "s-7k3f9m", Status: "harvested",
			PlotProgress: &protocol.SeedPlotProgress{Total: 3, Done: 1, Withered: 1, Growing: 1},
		},
	})
	out := buf.String()
	if !strings.Contains(out, "s-7k3f9m is harvested") {
		t.Fatalf("the move itself is not confirmed:\n%s", out)
	}
	if !strings.Contains(out, "1 open seed") {
		t.Fatalf("closing a crown with open children says nothing about them:\n%s", out)
	}
}

func TestFprintTransitionStaysQuietWhenNothingIsStranded(t *testing.T) {
	cases := []struct {
		name string
		seed protocol.Seed
	}{
		{"childless harvest", protocol.Seed{ID: "s-aaaaaa", Status: "harvested"}},
		{"open crown keeps growing", protocol.Seed{
			ID: "s-bbbbbb", Status: "growing",
			PlotProgress: &protocol.SeedPlotProgress{Total: 2, Growing: 2},
		}},
		{"crown closed after its plot", protocol.Seed{
			ID: "s-cccccc", Status: "harvested",
			PlotProgress: &protocol.SeedPlotProgress{Total: 2, Done: 1, Withered: 1},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			fprintTransition(&buf, &protocol.SeedTransitionResult{Seed: tc.seed})
			if strings.Contains(buf.String(), "open seed") {
				t.Fatalf("warned with nothing stranded:\n%s", buf.String())
			}
		})
	}
}
