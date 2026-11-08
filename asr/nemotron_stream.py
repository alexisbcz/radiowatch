# /// script
# requires-python = ">=3.11,<3.14"
# dependencies = [
#   "torch>=2.4",
#   "transformers>=5.18",
#   "numpy",
#   "librosa",
# ]
#
# [[tool.uv.index]]
# name = "pytorch-cpu"
# url = "https://download.pytorch.org/whl/cpu"
# explicit = true
#
# [tool.uv.sources]
# torch = { index = "pytorch-cpu" }
# ///
"""Live Persian speech recognition with Nemotron ASR Streaming (Farsi).

Audio in: raw 16 kHz mono signed 16-bit little-endian PCM, as it arrives.
Words out: JSON lines, as they are recognised.

    {"type": "ready"}
    {"type": "text", "text": "سلام ", "t": 12.4}     # t = seconds of audio consumed

Two modes:
  stdin   (default)    one stream: PCM on stdin, JSON on stdout
  --serve PORT         many streams: each TCP connection on 127.0.0.1:PORT is one
                       stream (PCM in, JSON out); all share one copy of the model

Model: https://huggingface.co/mehdi-hf/nemotron-asr-streaming-farsi
"""
import argparse
import json
import os
import socket
import sys
import threading
import warnings

import numpy as np

warnings.filterwarnings("ignore", message=".*max_length.*")

MODEL_ID = "mehdi-hf/nemotron-asr-streaming-farsi"
SR = 16000
READ_SAMPLES = 3200  # 0.2 s per read


def log(msg):
    print(f"[asr] {msg}", file=sys.stderr, flush=True)


class PCMBuffer:
    """Growing window over an audio stream, indexed by absolute sample number."""

    def __init__(self, read):
        self.read = read
        self.buf = np.zeros(0, dtype=np.float32)
        self.base = 0  # absolute index of buf[0]
        self.eof = False
        self.pending = b""

    def ensure(self, end):
        """Block until samples up to `end` are available. False at end of stream."""
        while self.base + len(self.buf) < end:
            if self.eof:
                return False
            raw = self.read(READ_SAMPLES * 2)
            if not raw:
                self.eof = True
                return False
            raw = self.pending + raw
            cut = len(raw) - len(raw) % 2
            self.pending = raw[cut:]
            pcm = np.frombuffer(raw[:cut], dtype="<i2").astype(np.float32) / 32768.0
            self.buf = np.concatenate([self.buf, pcm])
        return True

    def get(self, start, end):
        return self.buf[start - self.base : end - self.base]

    def pad(self, n):
        self.buf = np.concatenate([self.buf, np.zeros(max(n, 0), dtype=np.float32)])

    def drop_before(self, start):
        k = start - self.base
        if k > SR * 30:  # keep memory bounded on long streams
            self.buf = self.buf[k:]
            self.base = start


class Recognizer:
    def __init__(self, lookahead, threads):
        import torch
        from transformers import AutoModelForRNNT, AutoProcessor
        from transformers.utils import logging as hf_logging

        hf_logging.set_verbosity_error()
        hf_logging.disable_progress_bar()
        torch.set_num_threads(threads)
        self.torch = torch
        log(f"loading {MODEL_ID} (first run downloads ~2.4 GB) …")
        self.processor = AutoProcessor.from_pretrained(MODEL_ID)
        self.model = AutoModelForRNNT.from_pretrained(MODEL_ID).eval()
        self.processor.set_num_lookahead_tokens(lookahead)
        log(f"ready (look-ahead {lookahead}, {threads} threads)")

    def stream_model(self):
        """A model object for one stream. Streaming generate() keeps per-stream state on
        the model object, so concurrent streams need separate objects; this one is built
        empty on the meta device and then shares the loaded weights (no extra memory)."""
        from transformers import AutoModelForRNNT

        with self.torch.device("meta"):
            m = AutoModelForRNNT.from_config(self.model.config)
        m.load_state_dict(self.model.state_dict(), assign=True)
        for name, buf in self.model.named_buffers():  # non-persistent buffers too
            mod, _, attr = name.rpartition(".")
            (m.get_submodule(mod) if mod else m).register_buffer(attr, buf, persistent=False)
        m.generation_config = self.model.generation_config
        return m.eval()

    def features(self, audio, first):
        return self.processor(audio, sampling_rate=SR, is_streaming=True, is_first_audio_chunk=first,
                              language="fa-IR", return_tensors="pt").to(self.model.device, dtype=self.model.dtype)

    def run(self, read, emit):
        """Transcribe one stream until it ends: read(n) -> bytes, emit(dict)."""
        from transformers import TextIteratorStreamer

        p = self.processor
        hop, n_fft = p.feature_extractor.hop_length, p.feature_extractor.n_fft
        n_first, n_chunk = p.num_samples_first_audio_chunk, p.num_samples_per_audio_chunk
        mel_first, mel_chunk = p.num_mel_frames_first_audio_chunk, p.num_mel_frames_per_audio_chunk

        pcm = PCMBuffer(read)
        if not pcm.ensure(n_first):
            return
        first = self.features(pcm.get(0, n_first), True)
        consumed = [n_first]  # samples handed to the model, for word timestamps

        def chunks():
            yield first.input_features[:, :mel_first, :]
            mel = mel_first
            start = mel * hop - n_fft // 2
            flushed = False
            while True:
                end = start + n_chunk
                if not pcm.ensure(end):
                    if flushed:
                        return
                    # End of stream: two chunks of silence release the held-back look-ahead.
                    pcm.pad(end + 2 * n_chunk - (pcm.base + len(pcm.buf)))
                    flushed = True
                yield self.features(pcm.get(start, end), False).input_features
                consumed[0] = end
                mel += mel_chunk
                start = mel * hop - n_fft // 2
                pcm.drop_before(start - n_fft)

        # group_tokens=False: RNN-T output must not have repeated tokens merged (a CTC rule).
        streamer = TextIteratorStreamer(p.tokenizer, skip_special_tokens=True, group_tokens=False)

        model = self.stream_model()

        def work():
            with self.torch.inference_mode():
                model.generate(**{**first, "input_features": chunks()}, streamer=streamer)

        worker = threading.Thread(target=work, daemon=True)
        worker.start()
        for text in streamer:
            if text:
                emit({"type": "text", "text": text, "t": round(consumed[0] / SR, 2)})
        worker.join()


def serve(rec, port):
    srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind(("127.0.0.1", port))
    srv.listen()
    print(json.dumps({"type": "ready", "port": srv.getsockname()[1]}), flush=True)

    def handle(conn):
        lock = threading.Lock()
        rf = conn.makefile("rb")

        def emit(obj):
            with lock:
                conn.sendall((json.dumps(obj, ensure_ascii=False) + "\n").encode())

        try:
            emit({"type": "ready"})
            rec.run(rf.read, emit)
        except (BrokenPipeError, ConnectionResetError):
            pass
        except Exception:
            import traceback
            log("stream failed:\n" + traceback.format_exc())
        finally:
            conn.close()

    while True:
        conn, _ = srv.accept()
        threading.Thread(target=handle, args=(conn,), daemon=True).start()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--lookahead", type=int, default=3,
                    help="look-ahead tokens: 3 = 0.32 s (default, low latency), 13 = 1.12 s (most accurate)")
    ap.add_argument("--threads", type=int, default=max(1, (os.cpu_count() or 4) // 2))
    ap.add_argument("--serve", type=int, metavar="PORT",
                    help="serve many streams over TCP on 127.0.0.1:PORT (0 = any free port)")
    args = ap.parse_args()

    rec = Recognizer(args.lookahead, args.threads)
    if args.serve is not None:
        serve(rec, args.serve)
        return

    def emit(obj):
        sys.stdout.write(json.dumps(obj, ensure_ascii=False) + "\n")
        sys.stdout.flush()

    emit({"type": "ready"})
    rec.run(sys.stdin.buffer.read, emit)


if __name__ == "__main__":
    try:
        main()
    except (KeyboardInterrupt, BrokenPipeError):
        pass
