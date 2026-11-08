package main

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Station is one monitorable radio source.
type Station struct {
	ID       string `json:"id"`       // provider-qualified, e.g. "ir:26"
	Name     string `json:"name"`     // English name
	Native   string `json:"native"`   // name in the original language
	Country  string `json:"country"`  // ISO 3166-1 alpha-2
	Language string `json:"language"` // ISO 639-1
	Owner    string `json:"owner"`    // who runs it, shown with every claim
	Focus    string `json:"focus"`    // news, talk, regional, culture, music, religious…
	provider Provider
}

// Provider lists and resolves the stations of one network. New countries and
// networks (Russian, Chinese radio…) are added by implementing this interface.
type Provider interface {
	Prefix() string
	Stations(ctx context.Context) ([]Station, error)
	Resolve(ctx context.Context, s Station) (string, error) // live stream URL (HLS playlist)
}

var providers = []Provider{iribProvider{}}

func allStations(ctx context.Context) ([]Station, error) {
	var out []Station
	for _, p := range providers {
		st, err := p.Stations(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Prefix(), err)
		}
		out = append(out, st...)
	}
	return out, nil
}

// findStation accepts "ir:26", or a bare channel number for the IRIB provider.
func findStation(ctx context.Context, id string) (Station, error) {
	if !strings.Contains(id, ":") {
		id = "ir:" + id
	}
	st, err := allStations(ctx)
	if err != nil {
		return Station{}, err
	}
	for _, s := range st {
		if s.ID == id {
			return s, nil
		}
	}
	return Station{}, fmt.Errorf("unknown station %q (see `radiowatch stations`)", id)
}

// resolveWithRetry: IRIB's player page intermittently returns no stream.
func resolveWithRetry(ctx context.Context, s Station) (string, error) {
	var err error
	for i := 0; i < 4; i++ {
		var u string
		if u, err = s.provider.Resolve(ctx, s); err == nil {
			return u, nil
		}
		if !sleep(ctx, time.Duration(2<<i)*time.Second) {
			return "", ctx.Err()
		}
	}
	return "", err
}

// ---------------------------------------------------------------------------
// IRIB (Islamic Republic of Iran Broadcasting), via radio.iranseda.ir

type iribProvider struct{}

func (iribProvider) Prefix() string { return "ir" }

// English names and focus for the national channels; provincial channels are
// named after their province.
var iribKnown = map[int][2]string{
	26: {"Radio Khabar (news)", "news"}, 29: {"Radio Goftogu (talk)", "talk"},
	11: {"Radio Iran", "news"}, 12: {"Radio Payam", "news"}, 20: {"Radio Tehran", "regional"},
	19: {"Radio Eghtesad (economy)", "economy"}, 13: {"Radio Javan (youth)", "general"},
	16: {"Radio Farhang (culture)", "culture"}, 21: {"Radio Ava (music)", "music"},
	17: {"Radio Salamat (health)", "general"}, 18: {"Radio Varzesh (sport)", "sport"},
	22: {"Radio Namayesh (drama)", "culture"}, 23: {"Radio Saba", "culture"},
	14: {"Radio Ma'aref", "religious"}, 15: {"Radio Quran", "religious"},
	28: {"Radio Telavat", "religious"}, 101: {"Radio Tartil", "religious"},
	203: {"Radio Ziarat", "religious"}, 720: {"Radio Varzesh Banovan", "sport"},
	201: {"IRIB English", "news"}, 202: {"IRIB Arabic", "news"},
	901: {"IRIB World Service", "news"}, 902: {"IRIB Dari", "news"},
}

var iribProvinces = map[string]string{
	"البرز": "Alborz", "خوزستان": "Khuzestan", "کردستان": "Kurdistan", "یزد": "Yazd",
	"مازندران": "Mazandaran", "چهارمحال و بختیاری": "Chaharmahal and Bakhtiari", "گلستان": "Golestan",
	"آذربایجان غربی": "West Azerbaijan", "آذربایجان شرقی": "East Azerbaijan", "اردبیل": "Ardabil",
	"کرمانشاه": "Kermanshah", "ایلام": "Ilam", "کرمان": "Kerman", "خلیج فارس": "Persian Gulf (Hormozgan)",
	"گیلان": "Gilan", "قم": "Qom", "مرکزی": "Markazi", "همدان": "Hamadan", "زنجان": "Zanjan",
	"سمنان": "Semnan", "کیش": "Kish", "کهکیلویه و بویراحمد": "Kohgiluyeh and Boyer-Ahmad",
	"بوشهر": "Bushehr", "فارس": "Fars", "اصفهان": "Isfahan", "لرستان": "Lorestan", "آبادان": "Abadan",
	"خراسان رضوی": "Razavi Khorasan", "خراسان جنوبی": "South Khorasan", "خراسان شمالی": "North Khorasan",
	"مهاباد": "Mahabad", "سیستان و بلوچستان": "Sistan and Baluchestan", "قزوین": "Qazvin",
	"کاشان": "Kashan", "مریوان": "Marivan", "مراغه": "Maragheh", "زابل": "Zabol",
}

var iribLinkRE = regexp.MustCompile(`(?s)<a[^>]*href="\.\./live/\?VALID=TRUE&ch=(\d+)"[^>]*>(.*?)</a>`)
var tagRE = regexp.MustCompile(`<[^>]+>`)

func (p iribProvider) Stations(ctx context.Context) ([]Station, error) {
	body, err := get(ctx, "https://radio.iranseda.ir/radiolist/?VALID=TRUE")
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	var out []Station
	for _, m := range iribLinkRE.FindAllSubmatch(body, -1) {
		ch, _ := strconv.Atoi(string(m[1]))
		native := strings.Join(strings.Fields(html.UnescapeString(tagRE.ReplaceAllString(string(m[2]), " "))), " ")
		if native == "" || seen[ch] || (ch >= 50 && ch <= 66) { // 50–66 are "Radio Nama" visual duplicates
			continue
		}
		seen[ch] = true
		s := Station{ID: fmt.Sprintf("ir:%d", ch), Native: native, Country: "IR", Language: "fa",
			Owner: "IRIB (Iranian state broadcaster)", provider: p}
		if k, ok := iribKnown[ch]; ok {
			s.Name, s.Focus = k[0], k[1]
		} else {
			prov := strings.TrimSpace(strings.TrimPrefix(native, "رادیو"))
			s.Name, s.Focus = "Radio "+prov, "regional"
			if en, ok := iribProvinces[prov]; ok {
				s.Name = "Radio " + en + " (provincial)"
			}
		}
		if ch == 201 {
			s.Language = "en"
		} else if ch == 202 {
			s.Language = "ar"
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no stations found on radio.iranseda.ir")
	}
	sort.SliceStable(out, func(i, j int) bool { return focusRank(out[i].Focus) < focusRank(out[j].Focus) })
	return out, nil
}

func focusRank(f string) int {
	for i, x := range []string{"news", "talk", "economy", "regional", "general", "culture", "sport", "music", "religious"} {
		if f == x {
			return i
		}
	}
	return 99
}

func (iribProvider) Resolve(ctx context.Context, s Station) (string, error) {
	ch := strings.TrimPrefix(s.ID, "ir:")
	body, err := get(ctx, "https://player.iranseda.ir/live-player/?VALID=TRUE&CH="+ch+"&t=b&auto=true")
	if err != nil {
		return "", err
	}
	if m := m3u8RE.Find(body); m != nil {
		return string(m), nil
	}
	return "", fmt.Errorf("no stream found on the player page for %s", s.ID)
}
