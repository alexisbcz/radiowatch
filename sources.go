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
	// Direct streams (Icecast, standard HLS) are read by ffmpeg itself; others
	// (IRIB's raw-AAC HLS) go through the built-in playlist follower.
	Direct   bool `json:"-"`
	provider Provider
}

// Provider lists and resolves the stations of one network. New countries and
// networks (Russian, Chinese radio…) are added by implementing this interface.
type Provider interface {
	Prefix() string
	Stations(ctx context.Context) ([]Station, error)
	Resolve(ctx context.Context, s Station) (string, error) // live stream URL (HLS playlist)
}

var providers = []Provider{iribProvider{}, staticProvider{"ru", russianStations}, staticProvider{"cn", chineseStations}}

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

// staticProvider serves a fixed list of stations with known stream URLs.
type staticProvider struct {
	prefix   string
	stations []staticStation
}

type staticStation struct {
	Station
	URL string
}

func (p staticProvider) Prefix() string { return p.prefix }

func (p staticProvider) Stations(ctx context.Context) ([]Station, error) {
	out := make([]Station, len(p.stations))
	for i, s := range p.stations {
		out[i] = s.Station
		out[i].Direct, out[i].provider = true, p
	}
	return out, nil
}

func (p staticProvider) Resolve(ctx context.Context, st Station) (string, error) {
	for _, s := range p.stations {
		if s.ID == st.ID {
			return s.URL, nil
		}
	}
	return "", fmt.Errorf("unknown station %s", st.ID)
}

// Russian state and state-aligned radio. VGTRK, Rossiya Segodnya and Zvezda are
// state-owned; the Crimea stations are run by the Russian-installed authorities.
var russianStations = []staticStation{
	{Station{ID: "ru:vesti", Name: "Vesti FM", Native: "Вести ФМ", Country: "RU", Language: "ru", Owner: "VGTRK (Russian state broadcaster)", Focus: "news"}, "https://icecast-vgtrk.cdnvideo.ru/vestifm_mp3_128kbps"},
	{Station{ID: "ru:rossii", Name: "Radio Rossii", Native: "Радио России", Country: "RU", Language: "ru", Owner: "VGTRK (Russian state broadcaster)", Focus: "news"}, "https://icecast-vgtrk.cdnvideo.ru/rrzonam_mp3_128kbps"},
	{Station{ID: "ru:sputnik", Name: "Radio Sputnik", Native: "Радио Спутник", Country: "RU", Language: "ru", Owner: "Rossiya Segodnya (Russian state media group)", Focus: "news"}, "https://icecast-rian.cdnvideo.ru/voicerus"},
	{Station{ID: "ru:zvezda", Name: "Radio Zvezda", Native: "Радио Звезда", Country: "RU", Language: "ru", Owner: "Zvezda (Russian Ministry of Defence)", Focus: "news"}, "https://zvezda-radio.mediacdn.ru/radio/zvezda/zvezda_128"},
	{Station{ID: "ru:mayak", Name: "Radio Mayak", Native: "Маяк", Country: "RU", Language: "ru", Owner: "VGTRK (Russian state broadcaster)", Focus: "talk"}, "https://icecast-vgtrk.cdnvideo.ru/mayakfm_mp3_128kbps"},
	{Station{ID: "ru:bfm", Name: "Business FM", Native: "Business FM", Country: "RU", Language: "ru", Owner: "Rumedia (private)", Focus: "economy"}, "https://bfm.hostingradio.ru:9075/fm"},
	{Station{ID: "ru:krym", Name: "Radio Krym (Crimea)", Native: "Радио Крым", Country: "RU", Language: "ru", Owner: "Crimea TV and Radio Company (Russian-installed authorities)", Focus: "regional"}, "http://fm.1tvcrimea.ru:19000/stream.mp3"},
	{Station{ID: "ru:sevastopol", Name: "Govorit Sevastopol (Crimea)", Native: "Говорит Севастополь", Country: "RU", Language: "ru", Owner: "Sevastopol city broadcaster (Russian-installed authorities)", Focus: "regional"}, "https://sevfm.ru:9006/govsev"},
}

// Chinese state radio: China National Radio (CNR) and the former China Radio
// International (CRI), both part of China Media Group (CMG).
var chineseStations = []staticStation{
	{Station{ID: "cn:cnr1", Name: "CNR-1 Voice of China", Native: "中国之声", Country: "CN", Language: "zh", Owner: "China National Radio (CMG, Chinese state broadcaster)", Focus: "news"}, "https://ngcdn001.cnr.cn/live/zgzs/index.m3u8"},
	{Station{ID: "cn:global", Name: "CMG Global News Radio", Native: "环球资讯广播", Country: "CN", Language: "zh", Owner: "China Media Group (Chinese state broadcaster)", Focus: "news"}, "https://sk.cri.cn/905.m3u8"},
	{Station{ID: "cn:cnr2", Name: "CNR-2 Voice of Economy", Native: "经济之声", Country: "CN", Language: "zh", Owner: "China National Radio (CMG, Chinese state broadcaster)", Focus: "economy"}, "https://ngcdn001.cnr.cn/live/jjzs/index.m3u8"},
	{Station{ID: "cn:zhonghua", Name: "Voice of Zhonghua (for Taiwan)", Native: "中华之声", Country: "CN", Language: "zh", Owner: "China National Radio (CMG, Chinese state broadcaster)", Focus: "cross-strait"}, "https://ngcdn001.cnr.cn/live/zhzs/index.m3u8"},
	{Station{ID: "cn:huaxia", Name: "Voice of Huaxia (for Taiwan, Hong Kong, Macau)", Native: "华夏之声", Country: "CN", Language: "zh", Owner: "China National Radio (CMG, Chinese state broadcaster)", Focus: "cross-strait"}, "https://ngcdn001.cnr.cn/live/hxzs/index.m3u8"},
	{Station{ID: "cn:beijing", Name: "Beijing News Radio", Native: "北京新闻广播", Country: "CN", Language: "zh", Owner: "Beijing Radio and Television Station (state)", Focus: "regional"}, "https://brtv-radiolive.rbc.cn/alive/fm945.m3u8"},
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
	for i, x := range []string{"news", "talk", "cross-strait", "economy", "regional", "general", "culture", "sport", "music", "religious"} {
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
