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
)

// The speech recognizer is a Python helper (Nemotron ASR Streaming, Persian
// fine-tune) run with `uv`, which installs its dependencies on first use. It
// serves any number of streams over a local TCP port with one copy of the model.
//
//go:embed asr/nemotron_stream.py
var nemotronScript []byte

// Word is one recognised word.
type Word struct {
	Text string
	T    float64   // seconds of stream audio consumed when it was emitted
	At   time.Time // wall-clock time it was received
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

func startASR(ctx context.Context, lookahead, threads int) (*asrServer, error) {
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
		"--lookahead", fmt.Sprint(lookahead), "--threads", fmt.Sprint(threads))
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
	log.Printf("loading the speech model (the first run downloads ~2.4 GB) …")
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
	log.Printf("speech model ready")
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
}

func (s *asrServer) Open(ctx context.Context) (*asrStream, error) {
	c, err := net.Dial("tcp", s.addr)
	if err != nil {
		return nil, err
	}
	st := &asrStream{conn: c.(*net.TCPConn), audio: make(chan []byte, 32), Words: make(chan Word, 256), done: make(chan struct{})}
	// AAC segments -> ffmpeg -> 16 kHz mono PCM -> recognizer.
	st.decoder = exec.Command("ffmpeg", "-v", "error", "-f", "aac", "-i", "pipe:0",
		"-ar", "16000", "-ac", "1", "-f", "s16le", "pipe:1")
	if st.decIn, err = st.decoder.StdinPipe(); err != nil {
		c.Close()
		return nil, err
	}
	pcm, err := st.decoder.StdoutPipe()
	if err != nil {
		c.Close()
		return nil, err
	}
	if err := st.decoder.Start(); err != nil {
		c.Close()
		return nil, fmt.Errorf("starting ffmpeg: %w", err)
	}
	go func() {
		io.Copy(st.conn, pcm)
		st.conn.CloseWrite() // end of audio: the recognizer flushes and closes
	}()
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
	go func() {
		defer close(st.Words)
		sc := bufio.NewScanner(st.conn)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			var ev struct {
				Type, Text string
				T          float64
			}
			if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Type != "text" {
				continue
			}
			for _, w := range strings.Fields(ev.Text) {
				st.Words <- Word{Text: w, T: ev.T, At: time.Now()}
			}
		}
	}()
	return st, nil
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
	done := make(chan struct{})
	go func() { _ = st.decoder.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = st.decoder.Process.Kill()
	}
	go func() {
		time.Sleep(10 * time.Second)
		st.conn.Close()
	}()
}
