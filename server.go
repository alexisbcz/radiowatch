package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed web/index.html
var dashboardHTML []byte

func (m *monitor) serve(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(dashboardHTML)
	})
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"events": m.store.list(parseFilter(r)), "note": reliabilityNote})
	})
	mux.HandleFunc("GET /api/events.geojson", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, toGeoJSON(m.store.list(parseFilter(r))))
	})
	mux.HandleFunc("GET /feed.xml", func(w http.ResponseWriter, r *http.Request) {
		m.rss(w, r, "radiowatch: claims on monitored radio", parseFilter(r))
	})
	mux.HandleFunc("GET /feed/breaking.xml", func(w http.ResponseWriter, r *http.Request) {
		f := parseFilter(r)
		f.Breaking = true
		m.rss(w, r, "radiowatch: breaking claims", f)
	})
	mux.HandleFunc("GET /api/stations", func(w http.ResponseWriter, r *http.Request) {
		var out []map[string]any
		for _, st := range m.states {
			st.mu.Lock()
			out = append(out, map[string]any{"station": st.Station, "connected": st.Connected, "since": st.Since,
				"lastWord": st.LastWord, "words": st.Words, "events": st.Events, "error": st.Error})
			st.mu.Unlock()
		}
		writeJSON(w, map[string]any{"stations": out})
	})
	mux.HandleFunc("GET /api/transcript", func(w http.ResponseWriter, r *http.Request) {
		minutes, _ := strconv.Atoi(r.URL.Query().Get("minutes"))
		if minutes <= 0 {
			minutes = 5
		}
		id := r.URL.Query().Get("station")
		for _, st := range m.states {
			if st.Station.ID != id {
				continue
			}
			st.mu.Lock()
			var words []string
			for _, wd := range st.tail {
				if time.Since(wd.At) <= time.Duration(minutes)*time.Minute {
					words = append(words, wd.Text)
				}
			}
			st.mu.Unlock()
			writeJSON(w, map[string]any{"station": st.Station, "minutes": minutes, "text": strings.Join(words, " ")})
			return
		}
		http.Error(w, "unknown station", http.StatusNotFound)
	})
	srv := &http.Server{Addr: m.cfg.listen, Handler: cors(mux)}
	go func() { <-ctx.Done(); srv.Close() }()
	log.Printf("dashboard and feeds on http://%s", displayAddr(m.cfg.listen))
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("http: %v", err)
	}
}

func displayAddr(a string) string {
	if strings.HasPrefix(a, ":") {
		return "localhost" + a
	}
	return a
}

func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

// parseFilter: ?since=2h|RFC3339 &station=ir:26 &kind=event_report,official_statement
// &breaking=1 &min_importance=3 &limit=100
func parseFilter(r *http.Request) eventFilter {
	q := r.URL.Query()
	f := eventFilter{Station: q.Get("station"), Breaking: q.Get("breaking") == "1" || q.Get("breaking") == "true", Limit: 200}
	if s := q.Get("since"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			f.Since = time.Now().Add(-d)
		} else if t, err := time.Parse(time.RFC3339, s); err == nil {
			f.Since = t
		}
	}
	if k := q.Get("kind"); k != "" {
		f.Kinds = map[string]bool{}
		for _, x := range strings.Split(k, ",") {
			f.Kinds[strings.TrimSpace(x)] = true
		}
	}
	f.MinImport, _ = strconv.Atoi(q.Get("min_importance"))
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		f.Limit = n
	}
	return f
}

func toGeoJSON(events []Event) map[string]any {
	feats := []map[string]any{}
	for _, e := range events {
		for _, l := range e.Locations {
			if l.Lat == 0 && l.Lon == 0 {
				continue
			}
			feats = append(feats, map[string]any{
				"type":     "Feature",
				"geometry": map[string]any{"type": "Point", "coordinates": []float64{l.Lon, l.Lat}},
				"properties": map[string]any{
					"id": e.ID, "title": e.Title, "summary": e.Summary, "kind": e.Kind, "category": e.Category,
					"breaking": e.Breaking, "importance": e.Importance, "claimedBy": e.ClaimedBy,
					"place": l.NameEn, "placeOriginal": l.NameOriginal, "admin1": l.Admin1,
					"station": e.Station.Name, "stationId": e.Station.ID, "country": e.Station.Country,
					"firstSeen": e.FirstSeen, "quote": e.Quote, "reliability": e.Reliability,
				},
			})
		}
	}
	return map[string]any{"type": "FeatureCollection", "features": feats}
}

type rssItem struct {
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	Description string   `xml:"description"`
	PubDate     string   `xml:"pubDate"`
	GUID        string   `xml:"guid"`
	Categories  []string `xml:"category"`
}

func (m *monitor) rss(w http.ResponseWriter, r *http.Request, title string, f eventFilter) {
	base := "http://" + r.Host
	var items []rssItem
	for _, e := range m.store.list(f) {
		var places []string
		for _, l := range e.Locations {
			places = append(places, l.NameEn)
		}
		desc := fmt.Sprintf("%s\n\nKind: %s · Category: %s · Importance: %d/5\nClaimed by: %s, on %s (%s)\nPlaces: %s\nQuote (original): %s\n\n%s",
			e.Summary, e.Kind, e.Category, e.Importance, e.ClaimedBy, e.Station.Name, e.Station.Owner,
			strings.Join(places, ", "), e.Quote, e.Reliability)
		prefix := ""
		if e.Breaking {
			prefix = "BREAKING · "
		}
		items = append(items, rssItem{
			Title:       fmt.Sprintf("%s[%s] %s", prefix, e.Station.Name, e.Title),
			Link:        base + "/api/events?since=24h#" + e.ID,
			Description: desc,
			PubDate:     e.FirstSeen.UTC().Format(time.RFC1123Z),
			GUID:        "radiowatch-" + e.ID,
			Categories:  []string{e.Kind, e.Category, e.Station.Country},
		})
	}
	type channel struct {
		Title       string    `xml:"title"`
		Link        string    `xml:"link"`
		Description string    `xml:"description"`
		Items       []rssItem `xml:"item"`
	}
	type feed struct {
		XMLName xml.Name `xml:"rss"`
		Version string   `xml:"version,attr"`
		Channel channel  `xml:"channel"`
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	enc.Encode(feed{Version: "2.0", Channel: channel{Title: title, Link: base,
		Description: "Claims extracted from live radio broadcasts. " + reliabilityNote, Items: items}})
}
