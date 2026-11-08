// farhang plays, records and live-transcribes/translates IRIB radio stations
// from radio.iranseda.ir.
//
// Stations are published as HLS: a master playlist pointing to a media playlist
// of ~10 s AAC (ADTS) segments. The program resolves the stream for a channel,
// follows the live playlist, pipes the raw AAC into a player and/or a file, and
// can stream the audio into a live Persian speech recognizer (Nemotron ASR
// Streaming, Farsi fine-tune) and translate each phrase with Ollama, printing a
// live bilingual transcript.
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
	"syscall"
	"time"
)

// Known channel ids on radio.iranseda.ir (see -list).
var channels = []struct {
	ID   int
	Name string
}{
	{16, "Farhang (culture)"}, {11, "Iran"}, {12, "Payam"}, {13, "Javan (youth)"},
	{14, "Ma'aref"}, {15, "Quran"}, {17, "Salamat (health)"}, {18, "Varzesh (sport)"},
	{19, "Eghtesad (economy)"}, {20, "Tehran"}, {21, "Ava (music)"}, {22, "Namayesh (drama)"},
	{23, "Saba"}, {26, "Khabar (news)"}, {28, "Telavat"}, {29, "Goftogu (talk)"},
	{201, "English"}, {202, "Arabic"},
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

func main() {
	ch := flag.Int("ch", 16, "iranseda channel id (16 = Radio Farhang)")
	streamURL := flag.String("url", "", "HLS playlist URL (skips channel lookup)")
	player := flag.String("player", "mpv", "player to pipe into: mpv, ffplay or none")
	out := flag.String("o", "", "also record the raw AAC stream to this file")
	list := flag.Bool("list", false, "list known channels and exit")

	// Live transcription / translation.
	transcribe := flag.Bool("transcribe", false, "show a live Persian transcript")
	translateTo := flag.String("translate", "", "also translate into this language, e.g. English or French")
	lookahead := flag.Int("lookahead", 3, "recognizer look-ahead: 3 = 0.32 s (fast), 13 = 1.12 s (most accurate)")
	asrThreads := flag.Int("asr-threads", 4, "CPU threads for the speech recognizer")
	ollamaURL := flag.String("ollama-url", "http://localhost:11434", "Ollama server URL")
	ollamaModel := flag.String("ollama-model", "translategemma:4b", "Ollama model used for translation")
	showFA := flag.Bool("fa", true, "print the Persian transcript")
	pause := flag.Duration("pause", 1500*time.Millisecond, "silence that ends a phrase")
	minWords := flag.Int("min-words", 8, "phrases shorter than this wait for a longer silence")
	maxWords := flag.Int("max-words", 25, "longest phrase before it is cut and translated")
	logFile := flag.String("log", "", "append the transcript (Markdown) to this file")
	flag.Parse()
	log.SetFlags(log.Ltime)

	if *list {
		for _, c := range channels {
			fmt.Printf("%4d  %s\n", c.ID, c.Name)
		}
		return
	}
	if *translateTo != "" {
		*transcribe = true
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	master := *streamURL
	if master == "" {
		var err error
		if master, err = resolve(ctx, *ch); err != nil {
			log.Fatalf("resolve: %v", err)
		}
	}
	log.Printf("stream: %s", master)

	var writers []io.Writer
	var cmd *exec.Cmd
	if *player != "none" {
		in, c, err := startPlayer(ctx, *player)
		if err != nil {
			log.Fatal(err)
		}
		defer in.Close()
		writers, cmd = append(writers, in), c
	}
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		writers = append(writers, f)
		log.Printf("recording to %s", *out)
	}

	var sink func([]byte)
	if *transcribe {
		lt, err := newLiveTranscriber(ctx, liveConfig{
			lookahead:   *lookahead,
			asrThreads:  *asrThreads,
			ollamaURL:   *ollamaURL,
			ollamaModel: *ollamaModel,
			translateTo: *translateTo,
			showFA:      *showFA,
			logFile:     *logFile,
			pause:       *pause,
			minWords:    *minWords,
			maxWords:    *maxWords,
		})
		if err != nil {
			log.Fatalf("transcription: %v", err)
		}
		defer lt.Close()
		sink = lt.Push
	}
	if len(writers) == 0 && sink == nil {
		log.Fatal("nothing to do: use a player, -o, -transcribe, or a combination")
	}

	out2 := io.MultiWriter(writers...)
	onSegment := func(data []byte) error {
		if len(writers) > 0 {
			if _, err := out2.Write(data); err != nil {
				return fmt.Errorf("output closed: %w", err)
			}
		}
		if sink != nil {
			sink(data)
		}
		return nil
	}

	log.Printf("streaming; Ctrl+C to stop")
	if err := stream(ctx, master, onSegment); err != nil {
		log.Printf("stopped: %v", err)
	}
	if cmd != nil {
		_ = cmd.Process.Signal(os.Interrupt)
		_ = cmd.Wait()
	}
}
