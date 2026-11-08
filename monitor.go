package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type monitorConfig struct {
	stations   []Station
	dataDir    string
	window     time.Duration // transcript window sent for extraction
	minWords   int           // skip windows with less speech (music)
	llm        llmConfig
	webhook    string
	webhookMin int // minimum importance for a webhook (breaking events always go)
	lookahead  int
	asrThreads int
	listen     string // HTTP address, "" = no server
}

type stationState struct {
	Station   Station   `json:"station"`
	Connected bool      `json:"connected"`
	Since     time.Time `json:"since"`
	LastWord  time.Time `json:"lastWord"`
	Words     int       `json:"words"`
	Events    int       `json:"events"`
	Error     string    `json:"error,omitempty"`

	mu      sync.Mutex
	window  []Word // words since the last extraction
	tail    []Word // last ~15 minutes, for the live transcript view
	started time.Time
}

type job struct {
	st         *stationState
	start, end time.Time
	text       string
}

type recentWindow struct {
	at    time.Time
	words map[string]bool
	name  string
}

type monitor struct {
	recentMu sync.Mutex
	recent   map[string]recentWindow // last extracted window per station
	cfg      monitorConfig
	asr      *asrServer
	ext      *extractor
	store    *eventStore
	states   []*stationState
	jobs     chan job
	httpc    *http.Client
}

func runMonitor(ctx context.Context, cfg monitorConfig) error {
	if err := os.MkdirAll(filepath.Join(cfg.dataDir, "transcripts"), 0o755); err != nil {
		return err
	}
	store, err := openStore(filepath.Join(cfg.dataDir, "events.jsonl"))
	if err != nil {
		return err
	}
	m := &monitor{cfg: cfg, recent: map[string]recentWindow{}, ext: newExtractor(cfg.llm), store: store, jobs: make(chan job, 64),
		httpc: &http.Client{Timeout: 20 * time.Second}}
	var langs []string
	countries := map[string]bool{}
	for _, s := range cfg.stations {
		if !contains(langs, s.Language) {
			langs = append(langs, s.Language)
		}
		countries[s.Country] = true
	}
	for c := range countries {
		for _, g := range gazetteersFor[c] {
			if err := loadGazetteer(ctx, g); err != nil {
				log.Printf("geocoding for %s disabled: %v", g, err)
			}
		}
	}
	if m.asr, err = startASR(ctx, cfg.lookahead, cfg.asrThreads, langs); err != nil {
		return err
	}
	defer m.asr.Close()

	for _, s := range cfg.stations {
		m.states = append(m.states, &stationState{Station: s})
	}
	if cfg.listen != "" {
		go m.serve(ctx)
	}
	go m.extractLoop(ctx)
	var wg sync.WaitGroup
	for _, st := range m.states {
		wg.Add(2)
		go func() { defer wg.Done(); m.runStation(ctx, st) }()
		go func() { defer wg.Done(); m.windowLoop(ctx, st) }()
	}
	log.Printf("monitoring %d station(s); transcript windows every %s; extraction with %s", len(m.states), cfg.window, cfg.llm.Model)
	wg.Wait()
	return nil
}

// runStation keeps one station connected, reconnecting with backoff.
func (m *monitor) runStation(ctx context.Context, st *stationState) {
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		err := m.connect(ctx, st)
		st.mu.Lock()
		st.Connected = false
		if err != nil && ctx.Err() == nil {
			st.Error = err.Error()
			log.Printf("%s: %v; reconnecting in %s", st.Station.Name, err, backoff)
		}
		st.mu.Unlock()
		if !sleep(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, 5*time.Minute)
	}
}

func (m *monitor) connect(ctx context.Context, st *stationState) error {
	url, err := resolveWithRetry(ctx, st.Station)
	if err != nil {
		return err
	}
	var asrs *asrStream
	if st.Station.Direct {
		asrs, err = m.asr.OpenURL(ctx, url, st.Station.Language)
	} else {
		asrs, err = m.asr.Open(ctx, st.Station.Language)
	}
	if err != nil {
		return err
	}
	defer asrs.Close()
	go func() {
		for w := range asrs.Words {
			st.mu.Lock()
			st.window = append(st.window, w)
			st.tail = append(st.tail, w)
			for len(st.tail) > 0 && time.Since(st.tail[0].At) > 15*time.Minute {
				st.tail = st.tail[1:]
			}
			st.Words++
			st.LastWord = w.At
			st.mu.Unlock()
		}
	}()
	st.mu.Lock()
	st.Connected, st.Since, st.Error = true, time.Now(), ""
	st.mu.Unlock()
	log.Printf("%s: connected (%s)", st.Station.Name, url)
	if st.Station.Direct {
		return asrs.Wait(ctx)
	}
	return stream(ctx, url, func(seg []byte) error {
		if !asrs.Push(seg) {
			log.Printf("%s: recognizer is behind; dropped 10 s of audio", st.Station.Name)
		}
		return nil
	})
}

// windowLoop cuts the transcript into windows, archives them, and queues them for extraction.
func (m *monitor) windowLoop(ctx context.Context, st *stationState) {
	t := time.NewTicker(m.cfg.window)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st.mu.Lock()
		words := st.window
		st.window = nil
		st.mu.Unlock()
		if len(words) == 0 {
			continue
		}
		start, end := words[0].At, words[len(words)-1].At
		text := joinWords(words)
		m.archive(st.Station, start, end, text)
		if speechAmount(text) < m.cfg.minWords {
			continue // mostly music or silence
		}
		if other := m.simulcastOf(st, text); other != "" {
			log.Printf("%s: same programme as %s (simulcast); transcript archived, not extracted twice", st.Station.Name, other)
			continue
		}
		select {
		case m.jobs <- job{st: st, start: start, end: end, text: text}:
		default:
			log.Printf("%s: extraction is behind; skipping a window (transcript archived)", st.Station.Name)
		}
	}
}

// simulcastOf reports which other station, if any, broadcast the same words in
// the last two windows. Networks like IRIB relay one programme on several
// channels; extracting it once saves LLM time, and the copies are not
// independent sources.
func (m *monitor) simulcastOf(st *stationState, text string) string {
	set := map[string]bool{}
	if isCJK(text) { // no spaces: compare character pairs
		r := []rune(compact(text))
		for i := 0; i+2 <= len(r); i++ {
			set[string(r[i:i+2])] = true
		}
	} else {
		for _, w := range strings.Fields(text) {
			set[w] = true
		}
	}
	m.recentMu.Lock()
	defer m.recentMu.Unlock()
	now := time.Now()
	for id, r := range m.recent {
		if id == st.Station.ID || now.Sub(r.at) > 2*m.cfg.window {
			continue
		}
		common := 0
		for w := range set {
			if r.words[w] {
				common++
			}
		}
		if len(set) > 0 && common*10 >= len(set)*6 {
			return r.name
		}
	}
	m.recent[st.Station.ID] = recentWindow{at: now, words: set, name: st.Station.Name}
	return ""
}

func (m *monitor) archive(s Station, start, end time.Time, text string) {
	dir := filepath.Join(m.cfg.dataDir, "transcripts", strings.ReplaceAll(s.ID, ":", "-"))
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, start.UTC().Format("2006-01-02")+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(map[string]any{"station": s.ID, "start": start.UTC(), "end": end.UTC(), "text": text})
	f.Write(append(b, '\n'))
}

// extractLoop runs extraction one window at a time (the LLM is the slow part).
func (m *monitor) extractLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-m.jobs:
			t0 := time.Now()
			recent := m.store.recentTitles(j.st.Station.ID, time.Now().Add(-3*time.Hour), 12)
			items, err := m.ext.extract(ctx, j.st.Station, j.start, j.end, j.text, recent)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("%s: extraction failed: %v", j.st.Station.Name, err)
				}
				continue
			}
			n := 0
			for _, it := range items {
				if m.record(j, it) {
					n++
				}
			}
			log.Printf("%s: window %s–%s: %d item(s), %d new (%.0fs)", j.st.Station.Name,
				j.start.Format("15:04"), j.end.Format("15:04"), len(items), n, time.Since(t0).Seconds())
		}
	}
}

func (m *monitor) record(j job, it Item) bool {
	if strings.TrimSpace(it.Headline) == "" {
		return false
	}
	verified := quoteFound(it.Quote, j.text)
	if !verified {
		// A quote the model cannot point to in the transcript: keep the item, but
		// never as breaking, and lower its importance.
		it.Breaking = false
		it.Importance = max(1, it.Importance-2)
	}
	if it.Kind != "event_report" && it.Kind != "official_statement" {
		it.Breaking = false
	}
	for i := range it.Locations {
		gaz.geocode(&it.Locations[i])
	}
	e := &Event{
		Title: it.Headline, Summary: it.Summary, Kind: it.Kind, Category: it.Category,
		Breaking: it.Breaking, Importance: min(5, max(1, it.Importance)), ClaimedBy: it.ClaimedBy,
		Actors: it.Actors, Locations: it.Locations, When: it.When, Quote: it.Quote, QuoteVerified: verified,
		Station: j.st.Station, FirstSeen: j.end, LastSeen: j.end, Window: [2]time.Time{j.start, j.end},
		Reliability: reliabilityNote, Source: "radiowatch",
	}
	ev, isNew := m.store.add(e)
	if !isNew {
		return false
	}
	j.st.mu.Lock()
	j.st.Events++
	j.st.mu.Unlock()
	printEvent(ev)
	if m.cfg.webhook != "" && (ev.Breaking || ev.Importance >= m.cfg.webhookMin) {
		go m.notify(ev)
	}
	return true
}

func (m *monitor) notify(e *Event) {
	b, _ := json.Marshal(e)
	resp, err := m.httpc.Post(m.cfg.webhook, "application/json", bytes.NewReader(b))
	if err != nil {
		log.Printf("webhook: %v", err)
		return
	}
	resp.Body.Close()
}

func printEvent(e *Event) {
	color, tag := "", strings.ToUpper(strings.ReplaceAll(e.Kind, "_", " "))
	switch {
	case e.Breaking:
		color, tag = "\033[1;31m", "BREAKING"
	case e.Importance >= 4:
		color = "\033[1;33m"
	case e.Kind == "analysis" || e.Kind == "foreign_media_citation":
		color = dim
	}
	var places []string
	for _, l := range e.Locations {
		places = append(places, l.NameEn)
	}
	where := ""
	if len(places) > 0 {
		where = " @ " + strings.Join(places, ", ")
	}
	fmt.Printf("%s%s  [%s] %s · %s%s (importance %d, %s)%s\n", color, e.FirstSeen.Format("15:04"), e.Station.Name,
		tag, e.Title, where, e.Importance, e.ClaimedBy, reset)
}
