package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

type liveConfig struct {
	lookahead   int
	asrThreads  int
	ollamaURL   string
	ollamaModel string
	translateTo string // empty = transcript only
	language    string // ISO 639-1 of the station
	directURL   string // set for stations ffmpeg reads directly
	showFA      bool
	logFile     string
	pause       time.Duration // silence that ends a phrase
	minWords    int           // shorter phrases wait for a longer silence
	maxWords    int           // hard cap on phrase length
}

// liveTranscriber streams audio into the recognizer, shows the words as
// they are recognised, cuts them into phrases and translates each phrase.
type liveTranscriber struct {
	cfg    liveConfig
	ctx    context.Context
	server *asrServer
	stream *asrStream
	tty    bool
	logW   io.WriteCloser
	httpc  *http.Client
	queue  chan phrase

	mu       sync.Mutex // guards terminal output and the current phrase
	phrase   []Word
	units    float64 // phrase length in word-equivalents
	started  time.Time
	lastWord time.Time
}

type phrase struct {
	at   time.Time
	text string
}

func newLiveTranscriber(ctx context.Context, cfg liveConfig) (*liveTranscriber, error) {
	for _, bin := range []string{"ffmpeg", "uv"} {
		if _, err := exec.LookPath(bin); err != nil {
			return nil, fmt.Errorf("%s not found in PATH", bin)
		}
	}
	lt := &liveTranscriber{
		cfg:   cfg,
		ctx:   ctx,
		queue: make(chan phrase, 16),
		tty:   term.IsTerminal(int(os.Stdout.Fd())),
		httpc: &http.Client{Timeout: 3 * time.Minute},
	}
	if cfg.translateTo != "" {
		if err := lt.checkOllama(); err != nil {
			return nil, err
		}
	}
	if cfg.logFile != "" {
		f, err := os.OpenFile(cfg.logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		lt.logW = f
		fmt.Fprintf(f, "\n## %s\n\n", time.Now().Format("2006-01-02 15:04"))
	}

	var err error
	if lt.server, err = startASR(ctx, cfg.lookahead, cfg.asrThreads, []string{cfg.language}); err != nil {
		return nil, err
	}
	if cfg.directURL != "" {
		lt.stream, err = lt.server.OpenURL(ctx, cfg.directURL, cfg.language)
	} else {
		lt.stream, err = lt.server.Open(ctx, cfg.language)
	}
	if err != nil {
		lt.server.Close()
		return nil, err
	}
	go lt.readWords()
	go lt.pauseWatcher()
	go lt.translateLoop()
	return lt, nil
}

// Push receives one AAC segment from the stream; it never blocks playback.
func (lt *liveTranscriber) Push(seg []byte) {
	if !lt.stream.Push(seg) {
		log.Printf("recognizer is behind; dropping 10 s of audio to stay live")
	}
}

func (lt *liveTranscriber) Close() {
	lt.stream.Close()
	lt.flush()
	lt.server.Close()
	if lt.logW != nil {
		lt.logW.Close()
	}
}

func (lt *liveTranscriber) readWords() {
	for w := range lt.stream.Words {
		lt.addWord(w)
	}
}

// wordUnits: a word counts 1; a Chinese or Japanese character piece counts 1/2 per character.
func wordUnits(w Word) float64 {
	if isCJK(w.Text) {
		return float64(len([]rune(w.Text))) / 2
	}
	return 1
}

func (lt *liveTranscriber) addWord(w Word) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	if len(lt.phrase) == 0 {
		lt.started = time.Now()
		if lt.tty && lt.cfg.showFA {
			fmt.Printf("%s%s%s  %s", dim, lt.started.Format("15:04:05"), reset, cyan)
		}
	}
	lt.phrase = append(lt.phrase, w)
	lt.units += wordUnits(w)
	lt.lastWord = time.Now()
	if lt.tty && lt.cfg.showFA {
		if w.Space && len(lt.phrase) > 1 {
			fmt.Print(" ")
		}
		fmt.Print(w.Text)
	}
	if lt.units >= float64(lt.cfg.maxWords) {
		lt.flushLocked()
	}
}

// pauseWatcher ends the current phrase when the speaker pauses.
func (lt *liveTranscriber) pauseWatcher() {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-lt.ctx.Done():
			return
		case <-t.C:
			lt.mu.Lock()
			gap := time.Since(lt.lastWord)
			if len(lt.phrase) > 0 && ((lt.units >= float64(lt.cfg.minWords) && gap > lt.cfg.pause) || gap > 3*lt.cfg.pause) {
				lt.flushLocked()
			}
			lt.mu.Unlock()
		}
	}
}

func (lt *liveTranscriber) flush() {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	lt.flushLocked()
}

func (lt *liveTranscriber) flushLocked() {
	if len(lt.phrase) == 0 {
		return
	}
	p := phrase{at: lt.started, text: joinWords(lt.phrase)}
	lt.phrase, lt.units = nil, 0
	if lt.cfg.showFA {
		if lt.tty {
			fmt.Print(reset + "\n")
		} else {
			fmt.Printf("%s  %s\n", p.at.Format("15:04:05"), p.text)
		}
	}
	if lt.cfg.translateTo == "" {
		lt.logLine(p, "")
		return
	}
	select {
	case lt.queue <- p:
	default:
		log.Printf("translation is behind; skipping a phrase")
		lt.logLine(p, "")
	}
}

func (lt *liveTranscriber) translateLoop() {
	for {
		select {
		case <-lt.ctx.Done():
			return
		case p := <-lt.queue:
			tr, err := lt.translate(p.text)
			if err != nil {
				if lt.ctx.Err() == nil {
					log.Printf("translate: %v", err)
				}
				continue
			}
			lt.printTranslation(p, tr)
		}
	}
}

// printTranslation prints a finished translation above the phrase currently
// being transcribed, then redraws that phrase.
func (lt *liveTranscriber) printTranslation(p phrase, tr string) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	indent := "          "
	if !lt.cfg.showFA {
		indent = dim + p.at.Format("15:04:05") + reset + "  "
	}
	if lt.tty {
		fmt.Print("\r\033[2K")
		fmt.Printf("%s%s%s%s\n", indent, bold, tr, reset)
		if len(lt.phrase) > 0 && lt.cfg.showFA {
			fmt.Printf("%s%s%s  %s%s ", dim, lt.started.Format("15:04:05"), reset, cyan, joinWords(lt.phrase))
		}
	} else {
		if !lt.cfg.showFA {
			indent = p.at.Format("15:04:05") + "  "
		}
		fmt.Printf("%s%s\n", indent, tr)
	}
	lt.logLine(p, tr)
}

func (lt *liveTranscriber) logLine(p phrase, tr string) {
	if lt.logW == nil {
		return
	}
	fmt.Fprintf(lt.logW, "- `%s` %s\n", p.at.Format("15:04:05"), p.text)
	if tr != "" {
		fmt.Fprintf(lt.logW, "  > %s\n", tr)
	}
}

func (lt *liveTranscriber) checkOllama() error {
	resp, err := lt.httpc.Get(lt.cfg.ollamaURL + "/api/tags")
	if err != nil {
		return fmt.Errorf("Ollama is not reachable at %s (start it with `ollama serve`): %w", lt.cfg.ollamaURL, err)
	}
	defer resp.Body.Close()
	var tags struct {
		Models []struct{ Name string } `json:"models"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&tags)
	for _, m := range tags.Models {
		if m.Name == lt.cfg.ollamaModel || strings.TrimSuffix(m.Name, ":latest") == lt.cfg.ollamaModel {
			return nil
		}
	}
	return fmt.Errorf("Ollama model %q is not installed (run `ollama pull %s`)", lt.cfg.ollamaModel, lt.cfg.ollamaModel)
}

var langCodes = map[string]string{
	"english": "en", "french": "fr", "german": "de", "spanish": "es", "italian": "it",
	"portuguese": "pt", "arabic": "ar", "turkish": "tr", "russian": "ru", "chinese": "zh",
	"japanese": "ja", "dutch": "nl", "hebrew": "he", "hindi": "hi", "urdu": "ur",
}

func (lt *liveTranscriber) translate(text string) (string, error) {
	lang := lt.cfg.translateTo
	src, srcCode := langName(lt.cfg.language), lt.cfg.language
	var msgs []map[string]string
	if strings.HasPrefix(lt.cfg.ollamaModel, "translategemma") {
		// TranslateGemma's prescribed prompt.
		target := lang
		if code, ok := langCodes[strings.ToLower(lang)]; ok {
			target = fmt.Sprintf("%s (%s)", lang, code)
		}
		msgs = []map[string]string{{"role": "user", "content": fmt.Sprintf(
			"You are a professional %[4]s (%[5]s) to %[1]s translator. Your goal is to accurately convey "+
				"the meaning and nuances of the original %[4]s text while adhering to %[2]s grammar, "+
				"vocabulary, and cultural sensitivities.\nProduce only the %[2]s translation, without any "+
				"additional explanations or commentary. Please translate the following %[4]s text into %[2]s:\n\n\n%[3]s",
			target, lang, text, src, srcCode)}}
	} else {
		msgs = []map[string]string{
			{"role": "system", "content": fmt.Sprintf("You translate a live %s radio broadcast into %s. "+
				"The text comes from automatic speech recognition and may contain errors. "+
				"Translate the meaning faithfully and naturally. Output only the %s translation.", src, lang, lang)},
			{"role": "user", "content": text},
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"model": lt.cfg.ollamaModel, "stream": false, "keep_alive": "30m",
		"options":  map[string]any{"temperature": 0.2},
		"messages": msgs,
	})
	req, _ := http.NewRequestWithContext(lt.ctx, http.MethodPost, lt.cfg.ollamaURL+"/api/chat", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := lt.httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama: %s %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var r struct {
		Message struct{ Content string } `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	return strings.Join(strings.Fields(r.Message.Content), " "), nil
}

const (
	dim   = "\033[2m"
	bold  = "\033[1m"
	cyan  = "\033[36m"
	reset = "\033[0m"
)
