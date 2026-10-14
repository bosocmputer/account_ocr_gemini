package batchocr

import (
	"fmt"
	"testing"

	"github.com/bosocmputer/account_ocr_gemini/internal/storage"
)

func refs(n int) []storage.DocumentImageGroupRef {
	out := make([]storage.DocumentImageGroupRef, n)
	for i := range out {
		out[i] = storage.DocumentImageGroupRef{GuidFixed: fmt.Sprintf("g%03d", i)}
	}
	return out
}

func TestCapEligible(t *testing.T) {
	cases := []struct {
		name     string
		total    int
		max      int
		wantKept int
		wantLeft int
	}{
		{"under the limit: everything is kept", 162, 500, 162, 0},
		{"exactly at the limit", 500, 500, 500, 0},
		{"over the limit: first max kept, the rest reported", 162, 100, 100, 62},
		{"the real case, limit 500, 700 documents", 700, 500, 500, 200},
		{"max 0 falls back to the hard cap, not to 'no limit'", 1500, 0, maxItemsHardCap, 1500 - maxItemsHardCap},
		{"negative max falls back to the hard cap", 1500, -5, maxItemsHardCap, 1500 - maxItemsHardCap},
		{"a limit above the hard cap is clamped to it", 1500, 5000, maxItemsHardCap, 1500 - maxItemsHardCap},
		{"nothing to do", 0, 500, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kept, left := capEligible(refs(c.total), c.max)
			if len(kept) != c.wantKept || left != c.wantLeft {
				t.Fatalf("kept %d left %d, want kept %d left %d", len(kept), left, c.wantKept, c.wantLeft)
			}
			if len(kept)+left != c.total {
				t.Fatalf("documents lost: %d kept + %d left != %d", len(kept), left, c.total)
			}
		})
	}
}

func TestCapEligibleKeepsTheFirstDocumentsInOrder(t *testing.T) {
	kept, _ := capEligible(refs(10), 3)
	for i, want := range []string{"g000", "g001", "g002"} {
		if kept[i].GuidFixed != want {
			t.Fatalf("position %d: got %s want %s", i, kept[i].GuidFixed, want)
		}
	}
}
