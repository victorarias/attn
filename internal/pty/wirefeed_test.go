package pty

// Placement diffing, away from any terminal. This is the observation half of
// the feed path: the worker never reads a kitty escape, it reads ghostty's
// placement set before and after and says what moved.

import (
	"reflect"
	"testing"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

func placement(image, id uint32, row int32, z int32) ghosttyvt.KittyPlacement {
	return ghosttyvt.KittyPlacement{
		ImageID:         image,
		PlacementID:     id,
		Z:               z,
		GridCols:        2,
		GridRows:        2,
		ViewportRow:     row,
		ViewportVisible: true,
		ImageGeneration: 1,
	}
}

func TestDiffKittyPlacements(t *testing.T) {
	one := placement(7, 1, 3, 0)
	two := placement(7, 2, 5, 0)
	other := placement(9, 1, 3, 0)

	cases := []struct {
		name          string
		before, after []ghosttyvt.KittyPlacement
		want          kittyPlacementDelta
	}{
		{
			name:  "a first placement is an addition",
			after: []ghosttyvt.KittyPlacement{one},
			want:  kittyPlacementDelta{Added: []ghosttyvt.KittyPlacement{one}},
		},
		{
			name:   "a deleted placement is a removal named by its key",
			before: []ghosttyvt.KittyPlacement{one, two},
			after:  []ghosttyvt.KittyPlacement{two},
			want: kittyPlacementDelta{Removed: []kittyPlacementKey{
				{ImageID: 7, PlacementID: 1},
			}},
		},
		{
			name:   "a placement that moved is an update, not an add and a remove",
			before: []ghosttyvt.KittyPlacement{one},
			after:  []ghosttyvt.KittyPlacement{placement(7, 1, 1, 0)},
			want:   kittyPlacementDelta{Updated: []ghosttyvt.KittyPlacement{placement(7, 1, 1, 0)}},
		},
		{
			name:   "a restacked placement is an update: every field counts, not a chosen few",
			before: []ghosttyvt.KittyPlacement{one},
			after:  []ghosttyvt.KittyPlacement{placement(7, 1, 3, 4)},
			want:   kittyPlacementDelta{Updated: []ghosttyvt.KittyPlacement{placement(7, 1, 3, 4)}},
		},
		{
			name:   "an unchanged set produces nothing",
			before: []ghosttyvt.KittyPlacement{one, two},
			after:  []ghosttyvt.KittyPlacement{one, two},
			want:   kittyPlacementDelta{},
		},
		{
			// Two placements of one image share an image id, and two images can
			// both carry placement id 1. Keying on either alone loses one of them.
			name:   "placements are identified by image and placement id together",
			before: []ghosttyvt.KittyPlacement{one},
			after:  []ghosttyvt.KittyPlacement{one, two, other},
			want:   kittyPlacementDelta{Added: []ghosttyvt.KittyPlacement{two, other}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := diffKittyPlacements(tc.before, tc.after)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("diff:\n got %+v\nwant %+v", got, tc.want)
			}
			if got.empty() != (len(tc.want.Added)+len(tc.want.Removed)+len(tc.want.Updated) == 0) {
				t.Errorf("empty() = %v for %+v", got.empty(), got)
			}
		})
	}
}

// Nothing to synthesize means no bytes at all, which is what lets the read loop
// skip a fan-out rather than send an empty message.
func TestAppendCSIWritesNothingForZero(t *testing.T) {
	if got := appendCSI(nil, 0, 'S'); len(got) != 0 {
		t.Errorf("appendCSI(0) = %q, want nothing", got)
	}
	if got := string(appendCSI(nil, 12, 'B')); got != "\x1b[12B" {
		t.Errorf("appendCSI(12,'B') = %q", got)
	}
}
