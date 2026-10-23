package service

import (
	"slices"
	"testing"

	"github.com/Arman2122/p-ui/internal/util/link"
)

func TestAssignStableTagsReservesExistingServers(t *testing.T) {
	t.Run("does not let an inserted link steal a stable tag", func(t *testing.T) {
		parsed := []link.Outbound{{"tag": "Poland"}, {"tag": "NewServer"}, {"tag": "Netherlands"}}
		prev := map[string]string{
			"id-poland":      "sub1-poland",
			"id-netherlands": "sub1-netherlands",
		}
		prevTagByIndex := map[int]string{0: "sub1-poland", 1: "sub1-netherlands"}

		got := assignStableTags(parsed, []string{"id-poland", "id-new", "id-netherlands"}, prev, prevTagByIndex, 1, "")
		want := []string{"sub1-poland", "sub1-newserver", "sub1-netherlands"}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("does not let a fresh tag steal a stable tag", func(t *testing.T) {
		parsed := []link.Outbound{{"tag": "Netherlands"}, {"tag": "Renamed"}}
		prev := map[string]string{"id-netherlands": "sub1-netherlands"}

		got := assignStableTags(parsed, []string{"id-new", "id-netherlands"}, prev, nil, 1, "")
		want := []string{"sub1-netherlands-1", "sub1-netherlands"}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("skips reserved tags while adding a suffix", func(t *testing.T) {
		parsed := []link.Outbound{{"tag": "Netherlands"}, {"tag": "First"}, {"tag": "Second"}}
		prev := map[string]string{
			"id-first":  "sub1-netherlands",
			"id-second": "sub1-netherlands-1",
		}

		got := assignStableTags(parsed, []string{"id-new", "id-first", "id-second"}, prev, nil, 1, "")
		want := []string{"sub1-netherlands-2", "sub1-netherlands", "sub1-netherlands-1"}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
}
