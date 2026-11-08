package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const userAgent = "Mozilla/5.0 (X11; Linux x86_64) farhang/1.0"

var client = &http.Client{Timeout: 20 * time.Second}

func get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

var m3u8RE = regexp.MustCompile(`https://[^"'\s<>]+\.m3u8`)

// resolve finds the HLS master playlist for a channel from the official player page.
func resolve(ctx context.Context, ch int) (string, error) {
	page := fmt.Sprintf("https://player.iranseda.ir/live-player/?VALID=TRUE&CH=%d&t=b&auto=true", ch)
	body, err := get(ctx, page)
	if err != nil {
		return "", err
	}
	if m := m3u8RE.Find(body); m != nil {
		return string(m), nil
	}
	return "", fmt.Errorf("no stream found on player page for channel %d", ch)
}

type segment struct {
	seq int
	uri string
}

// parseMedia parses a media playlist into segments, the target duration and end-of-list flag.
func parseMedia(base *url.URL, body []byte) (segs []segment, target time.Duration, ended bool, err error) {
	seq, target := 0, 10*time.Second
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			seq, _ = strconv.Atoi(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"))
		case strings.HasPrefix(line, "#EXT-X-TARGETDURATION:"):
			if n, e := strconv.Atoi(strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:")); e == nil {
				target = time.Duration(n) * time.Second
			}
		case line == "#EXT-X-ENDLIST":
			ended = true
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			ref, e := base.Parse(line)
			if e != nil {
				return nil, 0, false, e
			}
			segs = append(segs, segment{seq, ref.String()})
			seq++
		}
	}
	return segs, target, ended, sc.Err()
}

// mediaPlaylist follows a master playlist to its first variant (or returns it if already a media playlist).
func mediaPlaylist(ctx context.Context, master string) (string, error) {
	body, err := get(ctx, master)
	if err != nil {
		return "", err
	}
	if !strings.Contains(string(body), "#EXT-X-STREAM-INF") {
		return master, nil
	}
	base, _ := url.Parse(master)
	lines := strings.Split(string(body), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "#EXT-X-STREAM-INF") && i+1 < len(lines) {
			ref, err := base.Parse(strings.TrimSpace(lines[i+1]))
			if err != nil {
				return "", err
			}
			return ref.String(), nil
		}
	}
	return "", errors.New("master playlist has no variants")
}

// stream follows the live playlist and hands every new segment, in order, to onSegment.
func stream(ctx context.Context, master string, onSegment func([]byte) error) error {
	media, err := mediaPlaylist(ctx, master)
	if err != nil {
		return err
	}
	base, _ := url.Parse(media)
	next := -1 // next sequence number to fetch
	fails := 0
	for {
		body, err := get(ctx, media)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if fails++; fails > 5 {
				return err
			}
			log.Printf("playlist: %v (retrying)", err)
			sleep(ctx, 2*time.Second)
			// The server may have rotated the chunklist name; re-resolve it.
			if m, e := mediaPlaylist(ctx, master); e == nil {
				media = m
				base, _ = url.Parse(m)
			}
			continue
		}
		fails = 0
		segs, target, ended, err := parseMedia(base, body)
		if err != nil {
			return err
		}
		if next < 0 && len(segs) > 0 {
			// Start near the live edge, keeping a little buffer.
			start := max(len(segs)-3, 0)
			next = segs[start].seq
		}
		for _, s := range segs {
			if s.seq < next {
				continue
			}
			data, err := get(ctx, s.uri)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				log.Printf("segment %d: %v (skipping)", s.seq, err)
				next = s.seq + 1
				continue
			}
			if err := onSegment(data); err != nil {
				return err
			}
			next = s.seq + 1
		}
		if ended {
			return nil
		}
		if !sleep(ctx, target/2) {
			return nil
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
