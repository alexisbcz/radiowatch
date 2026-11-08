package main

import (
	"bufio"
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
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"
)

// The speech recognizer runs as a small Python helper (Nemotron ASR Streaming,
// Persian fine-tune) started with `uv run`, so its dependencies install themselves.
//
//go:embed asr/nemotron_stream.py
var nemotronScript []byte

type liveConfig struct {
	lookahead   int
	asrThreads  int
	ollamaURL   string
	ollamaModel string
	translateTo string // empty = Persian transcript only
	showFA      bool
	logFile     string
	pause       time.Duration // silence that ends a phrase
	minWords    int           // shorter phrases wait for a longer silence
	maxWords    int           // hard cap on phrase length
}

// liveTranscriber streams audio into the recognizer, shows the Persian words as
// they are recognised, cuts them into phrases and translates each phrase.
type liveTranscriber struct {
	cfg     liveConfig
	ctx     context.Context
	audio   chan []byte
	decoder *exec.Cmd
	asr     *exec.Cmd
	decIn   io.WriteCloser
	tty     bool
	logW    io.WriteCloser
	httpc   *http.Client
	queue   chan phrase

	mu       sync.Mutex // guards terminal output and the current phrase
	phrase   []string
	started  time.Time
	lastWord time.Time
}

type phrase struct {
	at   time.Time
	text string
}

// scriptPath writes the embedded recognizer script to the user cache so that
// `uv run --script` can execute it (and cache its environment).
func scriptPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "farhang")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "nemotron_stream.py")
	if old, err := os.ReadFile(p); err != nil || !bytes.Equal(old, nemotronScript) {
		if err := os.WriteFile(p, nemotronScript, 0o644); err != nil {
			return "", err
		}
	}
	return p, nil
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
		audio: make(chan []byte, 32),
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

	// AAC segments -> ffmpeg -> 16 kHz mono PCM -> recognizer -> words (JSON lines).
	script, err := scriptPath()
	if err != nil {
		return nil, err
	}
	lt.decoder = exec.Command("ffmpeg", "-v", "error", "-f", "aac", "-i", "pipe:0",
		"-ar", "16000", "-ac", "1", "-f", "s16le", "pipe:1")
	lt.asr = exec.Command("uv", "run", "-q", "--script", script,
		"--lookahead", fmt.Sprint(cfg.lookahead), "--threads", fmt.Sprint(cfg.asrThreads))
	if lt.decIn, err = lt.decoder.StdinPipe(); err != nil {
		return nil, err
	}
	pcm, err := lt.decoder.StdoutPipe()
	if err != nil {
		return nil, err
	}
	lt.asr.Stdin = pcm
	lt.asr.Stderr = os.Stderr
	// Own process group: `uv` does not forward signals to the Python process it starts.
	lt.asr.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	lt.asr.Env = append(os.Environ(), "HF_HUB_DISABLE_PROGRESS_BARS=1", "TRANSFORMERS_VERBOSITY=error",
		"TQDM_DISABLE=1")
	words, err := lt.asr.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := lt.decoder.Start(); err != nil {
		return nil, fmt.Errorf("starting ffmpeg: %w", err)
	}
	if err := lt.asr.Start(); err != nil {
		return nil, fmt.Errorf("starting recognizer: %w", err)
	}

	ready := make(chan struct{})
	go lt.readWords(words, ready)
	go lt.feedAudio()
	go lt.pauseWatcher()
	go lt.translateLoop()

	log.Printf("loading the speech model (the first run downloads it, ~2.4 GB) …")
	select {
	case <-ready:
		log.Printf("speech model ready")
	case <-ctx.Done():
	case <-time.After(30 * time.Minute):
		return nil, fmt.Errorf("speech model did not start")
	}
	return lt, nil
}

// Push receives one AAC segment from the stream; it never blocks playback.
func (lt *liveTranscriber) Push(seg []byte) {
	select {
	case lt.audio <- seg:
	default:
		log.Printf("recognizer is behind; dropping 10 s of audio to stay live")
	}
}

func (lt *liveTranscriber) feedAudio() {
	for {
		select {
		case <-lt.ctx.Done():
			return
		case seg := <-lt.audio:
			if _, err := lt.decIn.Write(seg); err != nil {
				return
			}
		}
	}
}

func (lt *liveTranscriber) Close() {
	lt.flush()
	if lt.decIn != nil {
		lt.decIn.Close() // ends ffmpeg, then the recognizer sees end of audio
	}
	for _, c := range []*exec.Cmd{lt.decoder, lt.asr} {
		if c == nil || c.Process == nil {
			continue
		}
		done := make(chan struct{})
		go func() { _ = c.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			if c == lt.asr {
				_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL) // uv and the Python helper
			} else {
				_ = c.Process.Kill()
			}
			<-done
		}
	}
	if lt.logW != nil {
		lt.logW.Close()
	}
}

func (lt *liveTranscriber) readWords(r io.Reader, ready chan struct{}) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var ev struct{ Type, Text string }
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "ready":
			close(ready)
		case "text":
			for _, w := range strings.Fields(ev.Text) {
				lt.addWord(w)
			}
		}
	}
}

func (lt *liveTranscriber) addWord(w string) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	if len(lt.phrase) == 0 {
		lt.started = time.Now()
		if lt.tty && lt.cfg.showFA {
			fmt.Printf("%s%s%s  %s", dim, lt.started.Format("15:04:05"), reset, cyan)
		}
	}
	lt.phrase = append(lt.phrase, w)
	lt.lastWord = time.Now()
	if lt.tty && lt.cfg.showFA {
		fmt.Print(w + " ")
	}
	if len(lt.phrase) >= lt.cfg.maxWords {
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
			if len(lt.phrase) > 0 && ((len(lt.phrase) >= lt.cfg.minWords && gap > lt.cfg.pause) || gap > 3*lt.cfg.pause) {
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
	p := phrase{at: lt.started, text: strings.Join(lt.phrase, " ")}
	lt.phrase = nil
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
			fmt.Printf("%s%s%s  %s%s ", dim, lt.started.Format("15:04:05"), reset, cyan, strings.Join(lt.phrase, " "))
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

func (lt *liveTranscriber) translate(fa string) (string, error) {
	lang := lt.cfg.translateTo
	var msgs []map[string]string
	if strings.HasPrefix(lt.cfg.ollamaModel, "translategemma") {
		// TranslateGemma's prescribed prompt.
		target := lang
		if code, ok := langCodes[strings.ToLower(lang)]; ok {
			target = fmt.Sprintf("%s (%s)", lang, code)
		}
		msgs = []map[string]string{{"role": "user", "content": fmt.Sprintf(
			"You are a professional Persian (fa) to %[1]s translator. Your goal is to accurately convey "+
				"the meaning and nuances of the original Persian text while adhering to %[2]s grammar, "+
				"vocabulary, and cultural sensitivities.\nProduce only the %[2]s translation, without any "+
				"additional explanations or commentary. Please translate the following Persian text into %[2]s:\n\n\n%[3]s",
			target, lang, fa)}}
	} else {
		msgs = []map[string]string{
			{"role": "system", "content": fmt.Sprintf("You translate a live Persian radio broadcast into %s. "+
				"The Persian comes from automatic speech recognition, without punctuation, and may contain "+
				"errors. Translate the meaning faithfully and naturally. Output only the %s translation.", lang, lang)},
			{"role": "user", "content": fa},
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
