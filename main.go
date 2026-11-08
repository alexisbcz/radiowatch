// radiowatch listens to radio stations live, transcribes and translates them,
// and extracts the news they broadcast as structured, geolocated claims.
//
//	radiowatch listen   one station: audio + live transcript + translation
//	radiowatch monitor  many stations, headless: transcripts, fact extraction,
//	                    events store, dashboard and JSON/GeoJSON/RSS feeds
//	radiowatch stations list the available stations
//
// Sources are providers (IRIB's radio.iranseda.ir today); speech recognition is
// Nemotron ASR Streaming (Persian); extraction and translation use any
// OpenAI-compatible LLM endpoint (Ollama by default).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
)

const usage = `radiowatch: live radio transcription, translation and news extraction

Usage:
  radiowatch listen   [flags]   listen to one station with a live transcript (default)
  radiowatch monitor  [flags]   monitor several stations and extract news events
  radiowatch stations [flags]   list available stations

Run "radiowatch <command> -h" for the flags of a command.
`

func main() {
	log.SetFlags(log.Ltime)
	args := os.Args[1:]
	cmd := "listen"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch cmd {
	case "listen":
		err = cmdListen(ctx, args)
	case "monitor":
		err = cmdMonitor(ctx, args)
	case "stations":
		err = cmdStations(ctx, args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func cmdStations(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("stations", flag.ExitOnError)
	focus := fs.String("focus", "", "only stations with this focus (news, talk, regional, …)")
	fs.Parse(args)
	st, err := allStations(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tFOCUS\tLANG\tNATIVE NAME")
	for _, s := range st {
		if *focus == "" || s.Focus == *focus {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.ID, s.Name, s.Focus, s.Language, s.Native)
		}
	}
	return tw.Flush()
}

func cmdMonitor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("monitor", flag.ExitOnError)
	stationsFlag := fs.String("stations", "ir:26,ir:29,ir:11", "comma-separated station ids (see `radiowatch stations`)")
	dataDir := fs.String("data", "radiowatch-data", "directory for transcripts and events")
	listen := fs.String("http", "127.0.0.1:8787", "dashboard and feeds address (empty to disable)")
	window := fs.Duration("window", 5*time.Minute, "transcript window sent for news extraction")
	minWords := fs.Int("min-words", 40, "skip windows with fewer words (music, silence)")
	llmURL := fs.String("llm-url", "http://localhost:11434/v1", "OpenAI-compatible endpoint for extraction (key: RADIOWATCH_LLM_KEY)")
	llmModel := fs.String("llm-model", "gemma3:4b", "model used for news extraction")
	webhook := fs.String("webhook", "", "POST each new breaking or important event (JSON) to this URL")
	webhookMin := fs.Int("webhook-min", 4, "minimum importance (1–5) for a webhook; breaking events always go")
	lookahead := fs.Int("lookahead", 13, "recognizer look-ahead: 13 = 1.12 s (more accurate), 3 = 0.32 s")
	asrThreads := fs.Int("asr-threads", 6, "CPU threads for speech recognition (shared by all stations)")
	fs.Parse(args)

	var stations []Station
	for _, id := range strings.Split(*stationsFlag, ",") {
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		s, err := findStation(ctx, id)
		if err != nil {
			return err
		}
		stations = append(stations, s)
	}
	if len(stations) == 0 {
		return fmt.Errorf("no stations to monitor")
	}
	return runMonitor(ctx, monitorConfig{
		stations: stations, dataDir: *dataDir, window: *window, minWords: *minWords,
		llm:     llmConfig{BaseURL: *llmURL, Model: *llmModel},
		webhook: *webhook, webhookMin: *webhookMin, lookahead: *lookahead, asrThreads: *asrThreads,
		listen: *listen,
	})
}

func cmdListen(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("listen", flag.ExitOnError)
	ch := fs.String("ch", "ir:26", "station id (see `radiowatch stations`); a bare number means an IRIB channel")
	streamURL := fs.String("url", "", "HLS playlist URL (skips the station lookup)")
	player := fs.String("player", "mpv", "player to pipe into: mpv, ffplay or none")
	out := fs.String("o", "", "also record the raw AAC stream to this file")
	transcribe := fs.Bool("transcribe", false, "show a live transcript")
	translateTo := fs.String("translate", "", "also translate into this language, e.g. English or French")
	lookahead := fs.Int("lookahead", 3, "recognizer look-ahead: 3 = 0.32 s (fast), 13 = 1.12 s (most accurate)")
	asrThreads := fs.Int("asr-threads", 4, "CPU threads for the speech recognizer")
	ollamaURL := fs.String("ollama-url", "http://localhost:11434", "Ollama server URL")
	ollamaModel := fs.String("ollama-model", "translategemma:4b", "Ollama model used for translation")
	showFA := fs.Bool("fa", true, "print the original-language transcript")
	pause := fs.Duration("pause", 1500*time.Millisecond, "silence that ends a phrase")
	minWords := fs.Int("min-words", 8, "phrases shorter than this wait for a longer silence")
	maxWords := fs.Int("max-words", 25, "longest phrase before it is cut and translated")
	logFile := fs.String("log", "", "append the transcript (Markdown) to this file")
	fs.Parse(args)
	if *translateTo != "" {
		*transcribe = true
	}

	master, lang, direct := *streamURL, "fa", *streamURL != "" && !strings.Contains(*streamURL, "iranseda")
	var station Station
	if master == "" {
		var err error
		if station, err = findStation(ctx, *ch); err != nil {
			return err
		}
		if master, err = resolveWithRetry(ctx, station); err != nil {
			return err
		}
		lang, direct = station.Language, station.Direct
		log.Printf("%s (%s): %s", station.Name, station.Owner, master)
	}
	if direct {
		return listenDirect(ctx, master, lang, *player, *transcribe, liveConfig{
			lookahead: *lookahead, asrThreads: *asrThreads, ollamaURL: *ollamaURL, ollamaModel: *ollamaModel,
			translateTo: *translateTo, showFA: *showFA, logFile: *logFile, pause: *pause,
			minWords: *minWords, maxWords: *maxWords, language: lang, directURL: master,
		})
	}

	var writers []io.Writer
	var cmd *exec.Cmd
	if *player != "none" {
		in, c, err := startPlayer(ctx, *player)
		if err != nil {
			return err
		}
		defer in.Close()
		writers, cmd = append(writers, in), c
	}
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		writers = append(writers, f)
		log.Printf("recording to %s", *out)
	}
	var sink func([]byte)
	if *transcribe {
		lt, err := newLiveTranscriber(ctx, liveConfig{
			lookahead: *lookahead, asrThreads: *asrThreads, ollamaURL: *ollamaURL, ollamaModel: *ollamaModel,
			translateTo: *translateTo, showFA: *showFA, logFile: *logFile, pause: *pause,
			minWords: *minWords, maxWords: *maxWords, language: lang,
		})
		if err != nil {
			return fmt.Errorf("transcription: %w", err)
		}
		defer lt.Close()
		sink = lt.Push
	}
	if len(writers) == 0 && sink == nil {
		return fmt.Errorf("nothing to do: use a player, -o, -transcribe, or a combination")
	}
	mw := io.MultiWriter(writers...)
	log.Printf("streaming; Ctrl+C to stop")
	for {
		err := stream(ctx, master, func(data []byte) error {
			if len(writers) > 0 {
				if _, err := mw.Write(data); err != nil {
					return fmt.Errorf("output closed: %w", err)
				}
			}
			if sink != nil {
				sink(data)
			}
			return nil
		})
		if err != errStalled || ctx.Err() != nil || *streamURL != "" {
			if err != nil {
				log.Printf("stopped: %v", err)
			}
			break
		}
		log.Printf("stream stalled; reconnecting")
		s, ferr := findStation(ctx, *ch)
		if ferr == nil {
			if u, rerr := resolveWithRetry(ctx, s); rerr == nil {
				master = u
			}
		}
	}
	if cmd != nil {
		_ = cmd.Process.Signal(os.Interrupt)
		_ = cmd.Wait()
	}
	return nil
}

// listenDirect plays and transcribes a stream that mpv and ffmpeg read
// themselves (Icecast, standard HLS).
func listenDirect(ctx context.Context, url, lang, player string, transcribe bool, cfg liveConfig) error {
	if cfg.translateTo != "" {
		transcribe = true
	}
	var cmd *exec.Cmd
	switch player {
	case "mpv":
		cmd = exec.CommandContext(ctx, "mpv", "--no-video", "--really-quiet", url)
	case "ffplay":
		cmd = exec.CommandContext(ctx, "ffplay", "-nodisp", "-loglevel", "error", url)
	case "none":
	default:
		return fmt.Errorf("unknown player %q (use mpv, ffplay or none)", player)
	}
	if !transcribe && cmd == nil {
		return fmt.Errorf("nothing to do: use a player, -transcribe, or both")
	}
	if transcribe {
		lt, err := newLiveTranscriber(ctx, cfg)
		if err != nil {
			return fmt.Errorf("transcription: %w", err)
		}
		defer lt.Close()
	}
	if cmd != nil {
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("starting %s: %w", player, err)
		}
		defer func() { _ = cmd.Process.Signal(os.Interrupt); _ = cmd.Wait() }()
	}
	log.Printf("streaming; Ctrl+C to stop")
	<-ctx.Done()
	return nil
}

func startPlayer(ctx context.Context, name string) (io.WriteCloser, *exec.Cmd, error) {
	var cmd *exec.Cmd
	switch name {
	case "mpv":
		cmd = exec.CommandContext(ctx, "mpv", "--no-video", "--really-quiet", "--cache=yes", "-")
	case "ffplay":
		cmd = exec.CommandContext(ctx, "ffplay", "-nodisp", "-loglevel", "error", "-i", "-")
	default:
		return nil, nil, fmt.Errorf("unknown player %q (use mpv, ffplay or none)", name)
	}
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("starting %s: %w", name, err)
	}
	return in, cmd, nil
}
