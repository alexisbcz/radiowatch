package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// The speech recognizer is a Python helper (Nemotron ASR Streaming, Persian
// fine-tune) run with `uv`, which installs its dependencies on first use. It
// serves any number of streams over a local TCP port with one copy of the model.
//
//go:embed asr/nemotron_stream.py
var nemotronScript []byte

// Word is one recognised word.
type Word struct {
	Text  string
	Space bool      // preceded by a space in the recognizer's output (false inside Chinese text)
	T     float64   // seconds of stream audio consumed when it was emitted
	At    time.Time // wall-clock time it was received
}

type asrServer struct {
	cmd  *exec.Cmd
	addr string
}

// scriptPath writes the embedded helper to the user cache so `uv run --script`
// can execute it (and cache its environment between runs).
func scriptPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "radiowatch")
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

func startASR(ctx context.Context, lookahead, threads int, preload []string) (*asrServer, error) {
	for _, bin := range []string{"ffmpeg", "uv"} {
		if _, err := exec.LookPath(bin); err != nil {
			return nil, fmt.Errorf("%s not found in PATH", bin)
		}
	}
	script, err := scriptPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("uv", "run", "-q", "--script", script, "--serve", "0",
		"--lookahead", fmt.Sprint(lookahead), "--threads", fmt.Sprint(threads),
		"--preload", strings.Join(preload, ","))
	// Own process group: `uv` does not forward signals to the Python process it starts.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), "HF_HUB_DISABLE_PROGRESS_BARS=1", "TRANSFORMERS_VERBOSITY=error", "TQDM_DISABLE=1")
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting the speech recognizer: %w", err)
	}
	s := &asrServer{cmd: cmd}
	log.Printf("loading speech models for %s (the first run downloads ~2.4 GB per model) …", strings.Join(preload, ", "))
	ready := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			var ev struct {
				Type string
				Port int
			}
			if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Type == "ready" {
				s.addr = fmt.Sprintf("127.0.0.1:%d", ev.Port)
				ready <- nil
				io.Copy(io.Discard, out)
				return
			}
		}
		ready <- errors.New("the speech recognizer exited before it was ready")
	}()
	select {
	case err := <-ready:
		if err != nil {
			s.Close()
			return nil, err
		}
	case <-ctx.Done():
		s.Close()
		return nil, ctx.Err()
	}
	log.Printf("speech models ready")
	return s, nil
}

func (s *asrServer) Close() {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = s.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
}

// asrStream is one audio stream being transcribed: push AAC segments in,
// receive words on Words (closed when the stream ends).
type asrStream struct {
	conn    *net.TCPConn
	decoder *exec.Cmd
	decIn   io.WriteCloser
	audio   chan []byte
	Words   chan Word
	done    chan struct{}
	ended   chan struct{} // closed when ffmpeg exits
	err     error
}

// Open starts a stream fed with AAC segments through Push (IRIB's HLS follower).
func (s *asrServer) Open(ctx context.Context, lang string) (*asrStream, error) {
	return s.open(lang, exec.Command("ffmpeg", "-v", "error", "-f", "aac", "-i", "pipe:0",
		"-ar", "16000", "-ac", "1", "-f", "s16le", "pipe:1"), true)
}

// OpenURL starts a stream that ffmpeg reads from a URL itself (Icecast, HLS).
func (s *asrServer) OpenURL(ctx context.Context, url, lang string) (*asrStream, error) {
	args := []string{"-v", "error"}
	if strings.HasPrefix(url, "http") && !strings.Contains(url, ".m3u8") {
		args = append(args, "-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "30")
	}
	args = append(args, "-i", url, "-ar", "16000", "-ac", "1", "-f", "s16le", "pipe:1")
	return s.open(lang, exec.Command("ffmpeg", args...), false)
}

func (s *asrServer) open(lang string, decoder *exec.Cmd, pushed bool) (*asrStream, error) {
	c, err := net.Dial("tcp", s.addr)
	if err != nil {
		return nil, err
	}
	header, _ := json.Marshal(map[string]string{"language": lang})
	if _, err := c.Write(append(header, '\n')); err != nil {
		c.Close()
		return nil, err
	}
	st := &asrStream{conn: c.(*net.TCPConn), audio: make(chan []byte, 32), Words: make(chan Word, 256),
		done: make(chan struct{}), ended: make(chan struct{}), decoder: decoder}
	if pushed {
		if st.decIn, err = decoder.StdinPipe(); err != nil {
			c.Close()
			return nil, err
		}
	}
	pcm, err := decoder.StdoutPipe()
	if err != nil {
		c.Close()
		return nil, err
	}
	var stderr strings.Builder
	decoder.Stderr = &stderr
	if err := decoder.Start(); err != nil {
		c.Close()
		return nil, fmt.Errorf("starting ffmpeg: %w", err)
	}
	go func() {
		io.Copy(st.conn, pcm)
		st.conn.CloseWrite() // end of audio: the recognizer flushes and closes
		st.err = decoder.Wait()
		if msg := strings.TrimSpace(stderr.String()); msg != "" && st.err != nil {
			st.err = fmt.Errorf("%v: %s", st.err, lastLine(msg))
		}
		close(st.ended)
	}()
	if pushed {
		go func() {
			for {
				select {
				case <-st.done:
					st.decIn.Close()
					return
				case seg := <-st.audio:
					if _, err := st.decIn.Write(seg); err != nil {
						return
					}
				}
			}
		}()
	}
	go func() {
		defer close(st.Words)
		sc := bufio.NewScanner(st.conn)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		spaceBefore := true
		for sc.Scan() {
			var ev struct {
				Type, Text string
				T          float64
			}
			if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Type != "text" {
				continue
			}
			for _, w := range splitWords(ev.Text, &spaceBefore) {
				w.T, w.At = ev.T, time.Now()
				st.Words <- w
			}
		}
	}()
	return st, nil
}

// splitWords splits recognizer output into words, remembering where the
// recognizer put spaces so the text can be rebuilt exactly (Chinese has none).
// spaceBefore carries a trailing space over to the next piece.
func splitWords(text string, spaceBefore *bool) []Word {
	var out []Word
	var cur strings.Builder
	space := *spaceBefore
	for _, r := range text {
		if unicode.IsSpace(r) {
			if cur.Len() > 0 {
				out = append(out, Word{Text: cur.String(), Space: space})
				cur.Reset()
			}
			space = true
			continue
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		out = append(out, Word{Text: cur.String(), Space: space})
		space = false
	}
	*spaceBefore = space
	return out
}

// joinWords rebuilds text from words with the recognizer's own spacing.
func joinWords(words []Word) string {
	var b strings.Builder
	for i, w := range words {
		if i > 0 && w.Space {
			b.WriteByte(' ')
		}
		b.WriteString(w.Text)
	}
	return b.String()
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// Wait blocks until the audio source ends (direct streams) or ctx is done.
func (st *asrStream) Wait(ctx context.Context) error {
	select {
	case <-st.ended:
		if st.err != nil {
			return fmt.Errorf("stream ended: %w", st.err)
		}
		return fmt.Errorf("stream ended")
	case <-ctx.Done():
		return nil
	}
}

// Push hands one AAC segment to the recognizer; it never blocks the caller.
func (st *asrStream) Push(seg []byte) bool {
	select {
	case st.audio <- seg:
		return true
	default:
		return false
	}
}

// Close ends the audio; remaining words are still delivered on Words.
func (st *asrStream) Close() {
	select {
	case <-st.done:
		return
	default:
		close(st.done)
	}
	if st.decIn == nil { // direct stream: stop ffmpeg reading the URL
		_ = st.decoder.Process.Signal(os.Interrupt)
	}
	select {
	case <-st.ended:
	case <-time.After(3 * time.Second):
		_ = st.decoder.Process.Kill()
	}
	go func() {
		time.Sleep(10 * time.Second)
		st.conn.Close()
	}()
}
