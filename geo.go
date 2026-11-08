package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// gazetteer geocodes place names with GeoNames (https://www.geonames.org,
// CC BY 4.0). The country dump includes Persian alternate names, so names can
// be matched as spoken on air as well as in English.
type gazetteer struct {
	mu     sync.RWMutex
	byName map[string][]place
}

type place struct {
	id         int
	name       string
	lat, lon   float64
	class      byte // P populated place, A admin area, T terrain, H water, S spot/facility
	country    string
	admin1     string
	population int
}

var gaz = &gazetteer{byName: map[string][]place{}}

// gazetteersFor lists the GeoNames country dumps to load for a station's
// country: its own, plus the places its radio reports on most.
var gazetteersFor = map[string][]string{"IR": {"IR"}, "RU": {"RU", "UA"}, "CN": {"CN", "TW"}}

// admin1 maps "IR.26"-style codes to province names, from GeoNames' admin1CodesASCII.txt.
var admin1 = map[string]string{}

// landmarks covers what a single-country gazetteer cannot: countries, seas and
// straits, and a few strategic sites. Keys are matched after placeKey(), so both
// English and Persian spellings are listed. Coordinates are approximate.
var landmarks = map[string][2]float64{}

func init() {
	add := func(lat, lon float64, names ...string) {
		for _, n := range names {
			landmarks[placeKey(n)] = [2]float64{lat, lon}
		}
	}
	add(32.0, 53.0, "Iran", "ایران")
	add(26.57, 56.25, "Strait of Hormuz", "Hormuz Strait", "Hormuz", "تنگه هرمز", "هرمز")
	add(12.58, 43.33, "Bab-el-Mandeb", "Bab el-Mandeb", "باب المندب")
	add(27.0, 51.0, "Persian Gulf", "خلیج فارس")
	add(24.5, 58.5, "Gulf of Oman", "Sea of Oman", "دریای عمان")
	add(20.0, 38.5, "Red Sea", "دریای سرخ")
	add(42.0, 51.0, "Caspian Sea", "دریای خزر")
	add(34.885, 50.996, "Fordow", "Fordo", "فردو")
	add(33.724, 51.727, "Natanz", "نطنز")
	add(33.69, 51.76, "Kuh-e Kolang Gaz La", "Kuh-e Kolang", "Pickaxe Mountain", "کوه کلنگ", "کوه کلنگ گز لا")
	add(28.83, 50.89, "Bushehr nuclear power plant")
	add(39.8, -98.6, "United States", "USA", "America", "آمریکا", "امریکا", "ایالات متحده")
	add(38.9, -77.04, "Washington", "واشنگتن")
	add(31.4, 35.0, "Israel", "اسرائیل")
	add(32.08, 34.78, "Tel Aviv", "تل آویو")
	add(31.78, 35.22, "Jerusalem", "Al-Quds", "قدس", "بیت المقدس")
	add(31.4, 34.4, "Gaza", "غزه")
	add(33.9, 35.9, "Lebanon", "لبنان")
	add(33.0, 44.0, "Iraq", "عراق")
	add(35.0, 38.5, "Syria", "سوریه")
	add(15.5, 47.5, "Yemen", "یمن")
	add(24.0, 45.0, "Saudi Arabia", "عربستان", "عربستان سعودی")
	add(24.0, 54.0, "United Arab Emirates", "UAE", "امارات")
	add(25.3, 51.2, "Qatar", "قطر")
	add(26.0, 50.55, "Bahrain", "بحرین")
	add(21.5, 57.0, "Oman", "عمان")
	add(39.0, 35.0, "Turkey", "Türkiye", "ترکیه")
	add(33.9, 67.7, "Afghanistan", "افغانستان")
	add(30.4, 69.3, "Pakistan", "پاکستان")
	add(33.68, 73.05, "Islamabad", "اسلام آباد")
	add(40.1, 47.6, "Azerbaijan", "Republic of Azerbaijan", "جمهوری آذربایجان")
	add(61.5, 105.3, "Russia", "روسیه")
	add(35.9, 104.2, "China", "چین")
	add(51.2, 10.4, "Germany", "آلمان")
	add(46.2, 2.2, "France", "فرانسه")
	add(55.4, -3.4, "United Kingdom", "Britain", "UK", "انگلیس", "بریتانیا")
	add(50.85, 4.35, "Europe", "اروپا")
	// Russia and Ukraine
	add(49.0, 32.0, "Ukraine", "Украина")
	add(52.0, 19.0, "Poland", "Польша")
	add(53.7, 28.0, "Belarus", "Беларусь")
	add(45.3, 34.4, "Crimea", "Крым")
	add(48.0, 37.8, "Donbas", "Donbass", "Донбасс")
	add(45.3, 36.5, "Kerch Strait", "Crimean Bridge", "Керченский пролив", "Крымский мост")
	add(43.4, 34.0, "Black Sea", "Чёрное море", "Черное море")
	add(46.0, 36.6, "Sea of Azov", "Азовское море")
	add(47.51, 34.59, "Zaporizhzhia nuclear power plant", "Zaporizhzhia NPP", "Запорожская АЭС")
	add(51.67, 35.6, "Kursk nuclear power plant", "Курская АЭС")
	add(50.85, 4.35, "NATO", "НАТО")
	// China and the region
	add(23.7, 121.0, "Taiwan", "台湾", "台灣")
	add(24.5, 119.5, "Taiwan Strait", "台湾海峡")
	add(12.0, 114.0, "South China Sea", "南海")
	add(29.0, 125.0, "East China Sea", "东海")
	add(16.5, 112.0, "Paracel Islands", "西沙群岛")
	add(10.0, 114.0, "Spratly Islands", "南沙群岛")
	add(25.75, 123.5, "Senkaku Islands", "Diaoyu Islands", "钓鱼岛")
	add(22.3, 114.17, "Hong Kong", "香港")
	add(22.2, 113.55, "Macau", "Macao", "澳门")
	add(36.2, 138.25, "Japan", "日本")
	add(35.9, 127.8, "South Korea", "韩国")
	add(40.3, 127.5, "North Korea", "朝鲜")
	add(12.9, 121.8, "Philippines", "菲律宾")
	add(14.1, 108.3, "Vietnam", "越南")
	add(20.6, 79.0, "India", "印度")
	// country names as said on Russian and Chinese radio
	add(39.8, -98.6, "США", "Соединённые Штаты", "美国")
	add(61.5, 105.3, "Россия", "俄罗斯")
	add(35.9, 104.2, "Китай", "КНР", "中国")
	add(32.0, 53.0, "Иран", "伊朗")
	add(49.0, 32.0, "乌克兰")
	add(31.4, 35.0, "Израиль", "以色列")
	add(31.4, 34.4, "Газа", "加沙")
}

func cachedDownload(ctx context.Context, url, name string) ([]byte, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "radiowatch")
	path := filepath.Join(dir, name)
	if data, err := os.ReadFile(path); err == nil {
		return data, nil
	}
	log.Printf("downloading %s (once) …", name)
	data, err := get(ctx, url)
	if err != nil {
		return nil, err
	}
	_ = os.MkdirAll(dir, 0o755)
	return data, os.WriteFile(path, data, 0o644)
}

// loadGazetteer downloads (once) and indexes the GeoNames dump for a country.
func loadGazetteer(ctx context.Context, country string) error {
	codes, err := cachedDownload(ctx, "https://download.geonames.org/export/dump/admin1CodesASCII.txt", "admin1CodesASCII.txt")
	if err != nil {
		return fmt.Errorf("gazetteer: %w", err)
	}
	for _, line := range strings.Split(string(codes), "\n") {
		if c := strings.Split(line, "\t"); len(c) >= 2 {
			admin1[c[0]] = c[1]
		}
	}
	data, err := cachedDownload(ctx, "https://download.geonames.org/export/dump/"+country+".zip", country+".zip")
	if err != nil {
		return fmt.Errorf("gazetteer: %w", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		if f.Name != country+".txt" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
		gaz.mu.Lock()
		defer gaz.mu.Unlock()
		n := 0
		for sc.Scan() {
			c := strings.Split(sc.Text(), "\t")
			if len(c) < 15 || !strings.Contains("PATHS", c[6]) || c[6] == "" {
				continue
			}
			p := place{name: c[1], class: c[6][0], admin1: admin1[c[8]+"."+c[10]], country: c[8]}
			p.id, _ = strconv.Atoi(c[0])
			p.lat, _ = strconv.ParseFloat(c[4], 64)
			p.lon, _ = strconv.ParseFloat(c[5], 64)
			p.population, _ = strconv.Atoi(c[14])
			names := append([]string{c[1], c[2]}, strings.Split(c[3], ",")...)
			seen := map[string]bool{}
			for _, nm := range names {
				k := placeKey(nm)
				if k == "" || seen[k] {
					continue
				}
				seen[k] = true
				gaz.byName[k] = append(gaz.byName[k], p)
			}
			n++
		}
		log.Printf("gazetteer: %d places loaded for %s", n, country)
		return sc.Err()
	}
	return fmt.Errorf("gazetteer: %s.txt missing from archive", country)
}

// placeKey normalises a place name in Latin or Persian script for lookup.
func placeKey(s string) string {
	s = normFa(s)
	for _, p := range []string{"شهر ", "استان ", "city of ", "province of "} {
		s = strings.TrimPrefix(s, p)
	}
	var b strings.Builder
	for _, c := range s {
		if unicode.IsLetter(c) || unicode.IsDigit(c) {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// classRank prefers cities, then provinces, then natural features and sites.
func classRank(c byte) int {
	return strings.IndexByte("PAHTS", c)
}

// countryCodes maps the country names the model writes to ISO codes.
var countryCodes = map[string]string{
	"iran": "IR", "ir": "IR", "ایران": "IR", "russia": "RU", "ru": "RU", "russian federation": "RU",
	"ukraine": "UA", "ua": "UA", "china": "CN", "cn": "CN", "prc": "CN", "taiwan": "TW", "tw": "TW",
}

// geocode fills in coordinates, trying the name as spoken first: landmarks and
// countries, then the loaded GeoNames dumps (restricted to the location's country
// when the model names one).
func (g *gazetteer) geocode(l *Location) {
	for _, nm := range []string{l.NameOriginal, l.NameEn} {
		if c, ok := landmarks[placeKey(nm)]; ok {
			l.Lat, l.Lon = c[0], c[1]
			return
		}
	}
	want := countryCodes[strings.ToLower(strings.TrimSpace(l.Country))]
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, nm := range []string{l.NameOriginal, l.NameEn} {
		var best *place
		for _, c := range g.byName[placeKey(nm)] {
			if want != "" && c.country != want {
				continue
			}
			if best == nil || classRank(c.class) < classRank(best.class) ||
				(c.class == best.class && c.population > best.population) {
				cc := c
				best = &cc
			}
		}
		if best != nil {
			l.Lat, l.Lon, l.GeonameID, l.Admin1 = best.lat, best.lon, best.id, best.admin1
			return
		}
	}
}
