package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Event is one claim extracted from a broadcast. Every event is a claim made
// on air by the station, not a verified fact.
type Event struct {
	ID            string       `json:"id"`
	Title         string       `json:"title"`
	Summary       string       `json:"summary"`
	Kind          string       `json:"kind"`     // event_report, official_statement, threat, analysis, foreign_media_citation, other
	Category      string       `json:"category"` // military, attack, explosion, air_defense, nuclear, …
	Breaking      bool         `json:"breaking"`
	Importance    int          `json:"importance"` // 1–5
	ClaimedBy     string       `json:"claimedBy"`
	Actors        []string     `json:"actors"`
	Locations     []Location   `json:"locations"`
	When          string       `json:"when"`
	Quote         string       `json:"quote"` // verbatim, original language
	QuoteVerified bool         `json:"quoteVerified"`
	Station       Station      `json:"station"`
	FirstSeen     time.Time    `json:"firstSeen"`
	LastSeen      time.Time    `json:"lastSeen"`
	Mentions      int          `json:"mentions"`
	AlsoOn        []string     `json:"alsoOn,omitempty"` // other stations that carried the same item
	Window        [2]time.Time `json:"window"`
	Reliability   string       `json:"reliability"`
	Source        string       `json:"source"`
}

const reliabilityNote = "Claim broadcast by state media; automatically transcribed and extracted; not independently verified."

type eventStore struct {
	mu     sync.RWMutex
	path   string
	events []*Event // oldest first
}

func openStore(path string) (*eventStore, error) {
	s := &eventStore{path: path}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	byID := map[string]*Event{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			if old, ok := byID[e.ID]; ok {
				*old = e // later lines update earlier ones
			} else {
				ev := e
				byID[e.ID] = &ev
				s.events = append(s.events, &ev)
			}
		}
	}
	return s, sc.Err()
}

func (s *eventStore) appendLine(e *Event) {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(e)
	f.Write(append(b, '\n'))
}

// add stores a new item, or merges it into a matching recent event: a repeat on
// the same station becomes a new mention; on another station, a corroboration.
// It returns the event and whether it is new.
func (s *eventStore) add(e *Event) (*Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.events) - 1; i >= 0; i-- {
		old := s.events[i]
		if e.FirstSeen.Sub(old.LastSeen) > 6*time.Hour {
			break
		}
		if !sameStory(old, e) {
			continue
		}
		old.LastSeen = e.LastSeen
		old.Mentions++
		if old.Station.ID != e.Station.ID && !contains(old.AlsoOn, e.Station.ID) {
			old.AlsoOn = append(old.AlsoOn, e.Station.ID)
		}
		if e.Importance > old.Importance {
			old.Importance = e.Importance
		}
		s.appendLine(old)
		return old, false
	}
	h := sha1.Sum([]byte(e.Station.ID + e.FirstSeen.Format(time.RFC3339) + e.Title))
	e.ID = hex.EncodeToString(h[:8])
	e.Mentions = 1
	s.events = append(s.events, e)
	s.appendLine(e)
	return e, true
}

// sameStory: same category and either a shared geocoded place or similar titles.
func sameStory(a, b *Event) bool {
	if a.Category != b.Category {
		return false
	}
	sharedPlace := false
	for _, x := range a.Locations {
		for _, y := range b.Locations {
			if x.GeonameID != 0 && x.GeonameID == y.GeonameID {
				sharedPlace = true
			}
		}
	}
	sim := jaccard(a.Title+" "+a.Summary, b.Title+" "+b.Summary)
	return sim >= 0.45 || (sharedPlace && sim >= 0.25)
}

func jaccard(a, b string) float64 {
	set := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
		}) {
			if len(w) > 3 {
				m[w] = true
			}
		}
		return m
	}
	x, y := set(a), set(b)
	if len(x) == 0 || len(y) == 0 {
		return 0
	}
	inter := 0
	for w := range x {
		if y[w] {
			inter++
		}
	}
	return float64(inter) / float64(len(x)+len(y)-inter)
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

type eventFilter struct {
	Since     time.Time
	Station   string
	Kinds     map[string]bool
	Breaking  bool
	MinImport int
	Limit     int
}

// list returns matching events, newest first.
func (s *eventStore) list(f eventFilter) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Event
	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		if !f.Since.IsZero() && e.LastSeen.Before(f.Since) {
			continue
		}
		if f.Station != "" && e.Station.ID != f.Station && !contains(e.AlsoOn, f.Station) {
			continue
		}
		if len(f.Kinds) > 0 && !f.Kinds[e.Kind] {
			continue
		}
		if f.Breaking && !e.Breaking {
			continue
		}
		if e.Importance < f.MinImport {
			continue
		}
		out = append(out, *e)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].FirstSeen.After(out[j].FirstSeen) })
	return out
}

// recentTitles feeds the model what it already reported, to avoid repeats.
func (s *eventStore) recentTitles(station string, since time.Time, n int) []string {
	var out []string
	for _, e := range s.list(eventFilter{Station: station, Since: since, Limit: n}) {
		out = append(out, e.Title)
	}
	return out
}
