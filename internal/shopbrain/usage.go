package shopbrain

import (
	"sort"
	"time"
)

type counter struct {
	name  string
	count int
	last  time.Time
}

func bump(m map[string]*counter, code, name string, when time.Time) {
	if code == "" {
		return
	}
	c := m[code]
	if c == nil {
		c = &counter{name: name}
		m[code] = c
	}
	c.count++
	if c.name == "" {
		c.name = name
	}
	if when.After(c.last) {
		c.last = when
	}
}

func toUsage(m map[string]*counter, names map[string]string) []CountUsage {
	out := make([]CountUsage, 0, len(m))
	for code, c := range m {
		n := names[code]
		if n == "" {
			n = c.name
		}
		out = append(out, CountUsage{Code: code, Name: n, Count: c.count, LastUsed: c.last})
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Count != out[b].Count {
			return out[a].Count > out[b].Count
		}
		return out[a].Code < out[b].Code
	})
	return out
}

// buildUsage counts how the shop actually uses its master data, lists accounts
// that were never used, and checks each old template against real bookings.
func buildUsage(m MasterData, entries []entry) Usage {
	accts, books, fmts, parties := map[string]*counter{}, map[string]*counter{}, map[string]*counter{}, map[string]*counter{}
	byFormat := map[string][]entry{}
	for _, e := range entries {
		for _, l := range e.lines {
			bump(accts, l.code, l.name, e.when)
		}
		bump(books, e.j.BookCode, "", e.when)
		bump(fmts, e.j.DocFormat, "", e.when)
		if e.party.Code != "" {
			bump(parties, e.party.Kind+":"+e.party.Code, e.party.Name, e.when)
		}
		byFormat[e.j.DocFormat] = append(byFormat[e.j.DocFormat], e)
	}

	var u Usage
	inChart := map[string]bool{}
	for _, a := range m.Accounts {
		inChart[a.Code] = true
		c := accts[a.Code]
		au := AccountUsage{Code: a.Code, Name: a.Name, InChart: true}
		if c != nil {
			au.Count, au.LastUsed = c.count, c.last
		}
		u.Accounts = append(u.Accounts, au)
	}
	for code, c := range accts {
		if !inChart[code] {
			u.Accounts = append(u.Accounts, AccountUsage{Code: code, Name: c.name, Count: c.count, LastUsed: c.last, InChart: false})
		}
	}
	sort.Slice(u.Accounts, func(a, b int) bool { return u.Accounts[a].Code < u.Accounts[b].Code })

	bookNames := map[string]string{}
	for _, b := range m.JournalBooks {
		bookNames[b.Code] = b.Name
	}
	tplNames := map[string]string{}
	for _, t := range m.Templates {
		tplNames[t.DocCode] = t.Description
	}
	u.Books = toUsage(books, bookNames)
	u.DocFormat = toUsage(fmts, tplNames)
	u.Parties = toUsage(parties, nil)

	for _, t := range m.Templates {
		allowed := map[string]bool{}
		for _, l := range t.Lines {
			for _, c := range []string{l.AccountCode, l.DebitAccount, l.CreditAccount} {
				if c != "" {
					allowed[c] = true
				}
			}
		}
		tu := TemplateUsage{DocCode: t.DocCode, Description: t.Description}
		shapes := map[string]int{}
		for _, e := range byFormat[t.DocCode] {
			tu.JournalsUsing++
			shapes[e.shape.Accounts]++
			ok := len(allowed) > 0
			for _, l := range e.lines {
				// Lines linked to the counterparty's own account are not listed in the template.
				if !allowed[l.code] {
					ok = false
					break
				}
			}
			if ok {
				tu.MatchCount++
			}
		}
		if tu.JournalsUsing > 0 {
			tu.MatchRate = round4(float64(tu.MatchCount) / float64(tu.JournalsUsing))
			best, n := "", 0
			for s, c := range shapes {
				if c > n || (c == n && s < best) {
					best, n = s, c
				}
			}
			tu.TopActual = best
		}
		u.Templates = append(u.Templates, tu)
	}
	return u
}
