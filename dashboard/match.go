package dashboard

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Local is a local project: its folder name and what its files reveal.
type Local struct {
	Name  string
	Hints Hints
}

// Matched is a dashboard project linked to a local one, with the reason shown to the user.
type Matched struct {
	Project Project
	Reason  string
	Auto    bool    // found by detection, not by an exact name or the project's own configuration
	Score   float64 // 0..1
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func normalise(s string) string { return nonAlnum.ReplaceAllString(strings.ToLower(s), "") }

// words that say little about which project it is: legal forms, "website" and friends
var stop = map[string]bool{
	"group": true, "groep": true, "nv": true, "bv": true, "bvba": true, "vzw": true, "sa": true, "the": true, "de": true, "het": true,
	"van": true, "website": true, "site": true, "web": true, "webshop": true, "shop": true, "portal": true, "platform": true, "multisite": true,
}

func tokens(s string) []string {
	var all, kept []string
	seen := map[string]bool{}
	for _, t := range nonAlnum.Split(strings.ToLower(s), -1) {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		all = append(all, t)
		if !stop[t] {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		return all
	}
	return kept
}

func subset(a, b []string) bool { // every token of a is in b
	in := map[string]bool{}
	for _, t := range b {
		in[t] = true
	}
	long := false
	for _, t := range a {
		if !in[t] {
			return false
		}
		long = long || len(t) >= 3
	}
	return long
}

func sameTokens(a, b []string) bool { return len(a) == len(b) && subset(a, b) }

func dice(a, b string) float64 {
	bg := func(s string) map[string]int {
		m := map[string]int{}
		for i := 0; i+2 <= len(s); i++ {
			m[s[i:i+2]]++
		}
		return m
	}
	ba, bb := bg(a), bg(b)
	common := 0
	for k, n := range ba {
		if m := bb[k]; m < n {
			common += m
		} else {
			common += n
		}
	}
	if total := len(a) - 1 + len(b) - 1; total > 0 {
		return 2 * float64(common) / float64(total)
	}
	return 0
}

// nameScore compares a local name with a dashboard name: the same words (1), all words of the local name inside the
// dashboard's ("agora" in "Agora Group", 0.85), the other way round (the dashboard name "VAI" inside the folder
// "collectie-vai", 0.7: too short a name to trust), or merely similar letters (up to 0.8, typos). Only the first two
// are trusted enough to match without asking.
func nameScore(local, dash string) (float64, string) {
	if normalise(local) == "" || normalise(dash) == "" {
		return 0, ""
	}
	tl, td := tokens(local), tokens(dash)
	switch {
	case normalise(local) == normalise(dash):
		return 1, "is the same name as"
	case sameTokens(tl, td): // equal once words like "group" or "website" are ignored
		return 0.97, "matches the name"
	case subset(tl, td):
		return 0.85, "is part of the name"
	case subset(td, tl):
		return 0.7, "contains the name"
	}
	if d := dice(normalise(local), normalise(dash)); d >= 0.75 {
		return 0.6 + (d-0.75)*0.8, "looks like the name"
	}
	return 0, ""
}

// Matcher links local projects to dashboard projects. Hints that many local projects share (a generic site name,
// a company-wide git group) say nothing about one of them and are ignored.
type Matcher struct {
	locals    []Local
	projects  []Project
	common    map[string]int // normalised hint -> how many local projects carry it
	customers map[string]int // normalised customer -> dashboard projects of that customer
}

func NewMatcher(locals []Local, projects []Project) *Matcher {
	m := &Matcher{locals: locals, projects: projects, common: map[string]int{}, customers: map[string]int{}}
	for _, l := range locals {
		seen := map[string]bool{}
		add := func(v string) {
			if n := normalise(v); n != "" && !seen[n] {
				seen[n] = true
				m.common[n]++
			}
		}
		for _, h := range l.Hints.Names {
			add(h.Value)
		}
		for _, g := range l.Hints.Groups {
			add(g)
		}
	}
	for _, p := range projects {
		if n := normalise(p.Customer); n != "" {
			m.customers[n]++
		}
	}
	return m
}

func (m *Matcher) generic(v string) bool { return m.common[normalise(v)] >= 3 }

type signal struct {
	score  float64
	reason string
}

// score rates how likely p is the dashboard project of l, with the strongest evidence as the reason.
func (m *Matcher) score(l Local, p Project) (float64, string) {
	var sigs []signal
	if s, why := nameScore(l.Name, p.Name); s > 0 {
		sigs = append(sigs, signal{s, fmt.Sprintf("the folder name %q %s %q", l.Name, why, p.Name)})
	}
	for _, h := range l.Hints.Names {
		if m.generic(h.Value) {
			continue
		}
		if s, why := nameScore(h.Value, p.Name); s > 0 {
			w := 0.95
			if h.Kind == "Drupal site name" {
				w = 1
			}
			sigs = append(sigs, signal{s * w, fmt.Sprintf("the %s %q %s %q", h.Kind, h.Value, why, p.Name)})
		}
	}
	if p.Customer != "" {
		for _, g := range l.Hints.Groups {
			if m.generic(g) {
				continue
			}
			tg, tc := tokens(g), tokens(p.Customer)
			var s float64
			switch {
			case sameTokens(tg, tc):
				s = 0.8
			case subset(tg, tc) || subset(tc, tg):
				s = 0.7
			}
			if s > 0 {
				if m.customers[normalise(p.Customer)] == 1 {
					s += 0.1 // the only project of that customer
				}
				sigs = append(sigs, signal{s, fmt.Sprintf("the git group %q matches the customer %q", g, p.Customer)})
			}
		}
	}
	if len(sigs) == 0 {
		return 0, ""
	}
	sort.Slice(sigs, func(i, j int) bool { return sigs[i].score > sigs[j].score })
	best, agree := sigs[0], 0
	for _, s := range sigs {
		if s.score >= 0.6 {
			agree++
		}
	}
	if agree >= 2 {
		best.score = min(1, best.score+0.1) // independent signals pointing at the same project
	}
	return best.score, best.reason
}

// Resolve links every local project it can. Explicit configuration wins, then an identical name, and only
// then detection from hints, which is accepted when one candidate clearly stands out for both sides.
func (m *Matcher) Resolve(overrides map[string]string) map[string]Matched {
	byName := map[string][]Project{}
	byID := map[string]Project{}
	for _, p := range m.projects {
		byName[normalise(p.Name)] = append(byName[normalise(p.Name)], p)
		byID[p.ID] = p
	}
	out := map[string]Matched{}
	used := map[string]bool{}
	for _, l := range m.locals {
		if o := overrides[l.Name]; o != "" {
			var p Project
			var ok bool
			if p, ok = byID[o]; !ok {
				if ps := byName[normalise(o)]; len(ps) == 1 {
					p, ok = ps[0], true
				}
			}
			if ok {
				out[l.Name] = Matched{Project: p, Reason: "set in the project's configuration", Score: 1}
				used[p.ID] = true
			}
			continue
		}
		if ps := byName[normalise(l.Name)]; len(ps) == 1 {
			out[l.Name] = Matched{Project: ps[0], Reason: "same name", Score: 1}
			used[ps[0].ID] = true
		}
	}

	type pair struct {
		l     Local
		p     Project
		score float64
		why   string
	}
	var pairs []pair
	bestFor := map[string][]float64{} // local name -> scores, project id -> scores (two maps in one, keys cannot clash)
	for _, l := range m.locals {
		if _, done := out[l.Name]; done || overrides[l.Name] != "" {
			continue
		}
		for _, p := range m.projects {
			if used[p.ID] {
				continue
			}
			if s, why := m.score(l, p); s > 0 {
				pairs = append(pairs, pair{l, p, s, why})
				bestFor["l:"+l.Name] = append(bestFor["l:"+l.Name], s)
				bestFor["p:"+p.ID] = append(bestFor["p:"+p.ID], s)
			}
		}
	}
	for _, v := range bestFor {
		sort.Sort(sort.Reverse(sort.Float64Slice(v)))
	}
	// the margin to the runner-up, from either side: two locals competing for one project is as ambiguous as two projects for one local
	clear := func(key string, s float64) bool {
		v := bestFor[key]
		return len(v) < 2 || v[1] <= s-0.1
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].score != pairs[j].score {
			return pairs[i].score > pairs[j].score
		}
		return pairs[i].l.Name+pairs[i].p.ID < pairs[j].l.Name+pairs[j].p.ID
	})
	for _, pr := range pairs {
		if pr.score < 0.85 || used[pr.p.ID] {
			continue
		}
		if _, done := out[pr.l.Name]; done {
			continue
		}
		if clear("l:"+pr.l.Name, pr.score) && clear("p:"+pr.p.ID, pr.score) {
			out[pr.l.Name] = Matched{Project: pr.p, Reason: pr.why, Auto: true, Score: pr.score}
			used[pr.p.ID] = true
		}
	}
	return out
}

// Suggest returns up to n likely dashboard projects for a local project that has no match, best first.
// Projects in taken (already linked to another local project) are left out.
func (m *Matcher) Suggest(name string, taken map[string]bool, n int) []Project {
	var l Local
	for _, c := range m.locals {
		if c.Name == name {
			l = c
		}
	}
	if l.Name == "" {
		l = Local{Name: name}
	}
	type scored struct {
		p Project
		s float64
	}
	var all []scored
	for _, p := range m.projects {
		if taken[p.ID] {
			continue
		}
		if s, _ := m.score(l, p); s >= 0.5 {
			all = append(all, scored{p, s})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].s != all[j].s {
			return all[i].s > all[j].s
		}
		return all[i].p.Name < all[j].p.Name
	})
	var out []Project
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, all[i].p)
	}
	return out
}
