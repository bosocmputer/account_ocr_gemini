package shopbrain

import "sort"

// maxDiffIDs caps each id list in the diff; the counts stay exact.
const maxDiffIDs = 200

// Compare reports what changed since a previous brain of the same shop:
// journals added / edited / deleted (by content hash, so an edit is caught
// even if updatedat did not move) and patterns whose winning shape changed.
func Compare(prev, cur *Brain) *Diff {
	d := &Diff{PrevGeneratedAt: prev.GeneratedAt, Added: []string{}, Changed: []string{}, Removed: []string{},
		PatternsChanged: []string{}, PatternsNew: []string{}, PatternsGone: []string{}}
	for id, h := range cur.Index {
		ph, ok := prev.Index[id]
		switch {
		case !ok:
			d.Added = append(d.Added, id)
		case ph != h:
			d.Changed = append(d.Changed, id)
		}
	}
	for id := range prev.Index {
		if _, ok := cur.Index[id]; !ok {
			d.Removed = append(d.Removed, id)
		}
	}

	prevWin := map[string]string{}
	for _, p := range prev.Patterns {
		if p.Winner != nil {
			prevWin[p.ID] = p.Winner.Shape.fullKey()
		}
	}
	curIDs := map[string]bool{}
	for _, p := range cur.Patterns {
		curIDs[p.ID] = true
		w, ok := prevWin[p.ID]
		switch {
		case !ok:
			d.PatternsNew = append(d.PatternsNew, p.ID)
		case p.Winner != nil && w != p.Winner.Shape.fullKey():
			d.PatternsChanged = append(d.PatternsChanged, p.ID)
		}
	}
	for id := range prevWin {
		if !curIDs[id] {
			d.PatternsGone = append(d.PatternsGone, id)
		}
	}
	for _, l := range []*[]string{&d.Added, &d.Changed, &d.Removed, &d.PatternsChanged, &d.PatternsNew, &d.PatternsGone} {
		sort.Strings(*l)
	}
	d.Totals = d.Counts()
	return d
}

// DiffCounts holds exact counts before the id lists are capped for output.
type DiffCounts struct {
	Added           int `json:"added"`
	Changed         int `json:"changed"`
	Removed         int `json:"removed"`
	PatternsChanged int `json:"patterns_changed"`
	PatternsNew     int `json:"patterns_new"`
	PatternsGone    int `json:"patterns_gone"`
}

func (d *Diff) Counts() DiffCounts {
	return DiffCounts{len(d.Added), len(d.Changed), len(d.Removed), len(d.PatternsChanged), len(d.PatternsNew), len(d.PatternsGone)}
}

func (d *Diff) Empty() bool { return d.Totals == DiffCounts{} }

// Capped trims the id lists for writing into brain.json.
func (d *Diff) Capped() *Diff {
	cp := *d
	for _, l := range []*[]string{&cp.Added, &cp.Changed, &cp.Removed, &cp.PatternsChanged, &cp.PatternsNew, &cp.PatternsGone} {
		if len(*l) > maxDiffIDs {
			*l = (*l)[:maxDiffIDs]
		}
	}
	return &cp
}
