package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
)

//go:embed prompts/extract.md
var extractPrompt string

//go:embed prompts/extract.schema.json
var extractSchema []byte

// llmConfig points at any OpenAI-compatible chat endpoint: Ollama (default),
// OpenRouter, OpenAI, Anthropic's compatibility endpoint, vLLM, llama.cpp…
type llmConfig struct {
	BaseURL string // e.g. http://localhost:11434/v1
	Model   string
	APIKey  string // from RADIOWATCH_LLM_KEY
}

// Item is one newsworthy item as returned by the model.
type Item struct {
	Headline   string     `json:"headline"`
	Summary    string     `json:"summary"`
	Kind       string     `json:"kind"`
	Category   string     `json:"category"`
	Breaking   bool       `json:"breaking"`
	Importance int        `json:"importance"`
	ClaimedBy  string     `json:"claimed_by"`
	Actors     []string   `json:"actors"`
	Locations  []Location `json:"locations"`
	When       string     `json:"when"`
	Quote      string     `json:"quote"`
}

type Location struct {
	NameEn       string  `json:"name_en"`
	NameOriginal string  `json:"name_original"`
	Country      string  `json:"country"`
	Lat          float64 `json:"lat,omitempty"`
	Lon          float64 `json:"lon,omitempty"`
	Admin1       string  `json:"admin1,omitempty"`
	GeonameID    int     `json:"geonameId,omitempty"`
}

type extractor struct {
	cfg   llmConfig
	httpc *http.Client
}

func newExtractor(cfg llmConfig) *extractor {
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("RADIOWATCH_LLM_KEY")
	}
	return &extractor{cfg: cfg, httpc: &http.Client{Timeout: 15 * time.Minute}}
}

// extract asks the model for the newsworthy items in one transcript window.
func (x *extractor) extract(ctx context.Context, st Station, start, end time.Time, text string, recent []string) ([]Item, error) {
	recentNote := ""
	if len(recent) > 0 {
		recentNote = "\nItems already reported from this station in the last hours (do not repeat them unless there is new information):\n- " +
			strings.Join(recent, "\n- ") + "\n"
	}
	sys := strings.NewReplacer(
		"{{station}}", st.Name, "{{country}}", countryName(st.Country), "{{language}}", langName(st.Language),
		"{{owner}}", st.Owner, "{{start}}", start.UTC().Format("2006-01-02 15:04"),
		"{{end}}", end.UTC().Format("15:04"), "{{recent}}", recentNote,
	).Replace(extractPrompt)

	var schema any
	_ = json.Unmarshal(extractSchema, &schema)
	payload, _ := json.Marshal(map[string]any{
		"model":       x.cfg.Model,
		"temperature": 0,
		"messages": []map[string]string{
			{"role": "system", "content": sys},
			{"role": "user", "content": text},
		},
		"response_format": map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "items", "schema": schema},
		},
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(x.cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if x.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+x.cfg.APIKey)
	}
	resp, err := x.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var r struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &r); err != nil || len(r.Choices) == 0 {
		return nil, fmt.Errorf("LLM: unexpected response: %.200s", body)
	}
	content := strings.TrimSpace(r.Choices[0].Message.Content)
	content = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(content, "```json"), "```"), "```")
	var out struct {
		Items []Item `json:"items"`
	}
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return nil, fmt.Errorf("LLM returned invalid JSON: %v", err)
	}
	return out.Items, nil
}

var langNames = map[string]string{"fa": "Persian", "ru": "Russian", "zh": "Chinese (Mandarin)", "en": "English",
	"ar": "Arabic", "uk": "Ukrainian", "tr": "Turkish"}

func langName(code string) string {
	if n, ok := langNames[code]; ok {
		return n
	}
	return code
}

var countryNames = map[string]string{"IR": "Iran", "RU": "Russia", "CN": "China"}

func countryName(code string) string {
	if n, ok := countryNames[code]; ok {
		return n
	}
	return code
}

// normFa folds Persian/Arabic letter variants and drops spacing, diacritics and
// punctuation, so quotes can be matched against the transcript.
func normFa(s string) string {
	r := strings.NewReplacer("ي", "ی", "ى", "ی", "ك", "ک", "ة", "ه", "ۀ", "ه", "أ", "ا", "إ", "ا", "آ", "ا", "‌", " ")
	s = r.Replace(strings.ToLower(s))
	var b strings.Builder
	for _, c := range s {
		if unicode.IsLetter(c) || unicode.IsDigit(c) || unicode.IsSpace(c) {
			b.WriteRune(c)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// quoteFound reports whether the quote really comes from the transcript: most
// of its words occur there, and at least one exact three-word run does (for
// scripts without spaces, such as Chinese, three-character runs). A guard
// against quotes (and so claims) the model made up.
func quoteFound(quote, transcript string) bool {
	if isCJK(transcript) {
		return runsFound([]rune(compact(quote)), []rune(compact(transcript)), 3)
	}
	q := strings.Fields(normFa(quote))
	t := strings.Fields(normFa(transcript))
	if len(q) == 0 || len(t) == 0 {
		return false
	}
	vocab := map[string]bool{}
	for _, w := range t {
		vocab[w] = true
	}
	hits := 0
	for _, w := range q {
		if vocab[w] {
			hits++
		}
	}
	if hits*10 < len(q)*7 {
		return false
	}
	if len(q) < 3 {
		return true
	}
	joined := " " + strings.Join(t, " ") + " "
	for i := 0; i+3 <= len(q); i++ {
		if strings.Contains(joined, " "+strings.Join(q[i:i+3], " ")+" ") {
			return true
		}
	}
	return false
}

// runsFound: at least 70% of the quote's n-character runs occur in the transcript.
func runsFound(q, t []rune, n int) bool {
	if len(q) < n {
		return strings.Contains(string(t), string(q)) && len(q) > 0
	}
	ts := string(t)
	hits, total := 0, 0
	for i := 0; i+n <= len(q); i++ {
		total++
		if strings.Contains(ts, string(q[i:i+n])) {
			hits++
		}
	}
	return hits*10 >= total*7
}

// compact keeps only letters and digits (no spaces or punctuation).
func compact(s string) string {
	var b strings.Builder
	for _, c := range normFa(s) {
		if unicode.IsLetter(c) || unicode.IsDigit(c) {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// isCJK reports whether text is mostly Chinese, Japanese or Korean characters.
func isCJK(s string) bool {
	cjk, letters := 0, 0
	for _, c := range s {
		if unicode.IsLetter(c) {
			letters++
			if unicode.Is(unicode.Han, c) || unicode.Is(unicode.Hiragana, c) || unicode.Is(unicode.Katakana, c) || unicode.Is(unicode.Hangul, c) {
				cjk++
			}
		}
	}
	return letters > 0 && cjk*2 > letters
}

// speechAmount estimates how much was said, in word-equivalents: words for
// spaced scripts, characters / 2 for Chinese and Japanese.
func speechAmount(text string) int {
	if isCJK(text) {
		return len([]rune(compact(text))) / 2
	}
	return len(strings.Fields(text))
}
