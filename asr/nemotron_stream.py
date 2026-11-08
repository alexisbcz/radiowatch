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

Reads raw audio from stdin (16 kHz, mono, signed 16-bit little-endian) as it
arrives, runs the model's cache-aware streaming inference, and writes JSON
lines to stdout as words are recognised:

    {"type": "ready"}
    {"type": "text", "text": "سلام "}

Model: https://huggingface.co/mehdi-hf/nemotron-asr-streaming-farsi
"""
import argparse
import json
import os
import sys
import threading
import warnings

import numpy as np

warnings.filterwarnings("ignore", message=".*max_length.*")

MODEL_ID = "mehdi-hf/nemotron-asr-streaming-farsi"
READ_SAMPLES = 3200  # 0.2 s per read


def emit(obj):
    sys.stdout.write(json.dumps(obj, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def log(msg):
    print(f"[nemotron] {msg}", file=sys.stderr, flush=True)


class PCMBuffer:
    """Growing window over the stdin audio stream, indexed by absolute sample number."""

    def __init__(self, stream):
        self.stream = stream
        self.buf = np.zeros(0, dtype=np.float32)
        self.base = 0  # absolute index of buf[0]
        self.eof = False

    def ensure(self, end):
        """Block until samples up to `end` (absolute) are available. False at end of stream."""
        while self.base + len(self.buf) < end:
            if self.eof:
                return False
            raw = self.stream.read(READ_SAMPLES * 2)
            if not raw:
                self.eof = True
                return False
            if len(raw) % 2:
                raw = raw[:-1]
            pcm = np.frombuffer(raw, dtype="<i2").astype(np.float32) / 32768.0
            self.buf = np.concatenate([self.buf, pcm])
        return True

    def get(self, start, end):
        return self.buf[start - self.base : end - self.base]

    def pad(self, n):
        """Append silence (used to flush the model's look-ahead when the stream ends)."""
        self.buf = np.concatenate([self.buf, np.zeros(n, dtype=np.float32)])

    def drop_before(self, start):
        k = start - self.base
        if k > 16000 * 30:  # keep memory bounded on long streams
            self.buf = self.buf[k:]
            self.base = start


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--lookahead", type=int, default=3,
                    help="look-ahead tokens: 3 = 0.32 s (default, low latency), 13 = 1.12 s (most accurate)")
    ap.add_argument("--threads", type=int, default=max(1, (os.cpu_count() or 4) // 2))
    args = ap.parse_args()

    import torch
    from transformers import AutoModelForRNNT, AutoProcessor, TextIteratorStreamer

    from transformers.utils import logging as hf_logging
    hf_logging.set_verbosity_error()
    hf_logging.disable_progress_bar()
    torch.set_num_threads(args.threads)
    log(f"loading {MODEL_ID} (first run downloads ~2.4 GB) …")
    processor = AutoProcessor.from_pretrained(MODEL_ID)
    model = AutoModelForRNNT.from_pretrained(MODEL_ID).eval()
    processor.set_num_lookahead_tokens(args.lookahead)

    sr = processor.feature_extractor.sampling_rate
    hop = processor.feature_extractor.hop_length
    n_fft = processor.feature_extractor.n_fft
    n_first = processor.num_samples_first_audio_chunk
    n_chunk = processor.num_samples_per_audio_chunk
    mel_first = processor.num_mel_frames_first_audio_chunk
    mel_chunk = processor.num_mel_frames_per_audio_chunk

    def features(audio, first):
        return processor(audio, sampling_rate=sr, is_streaming=True, is_first_audio_chunk=first,
                         language="fa-IR", return_tensors="pt").to(model.device, dtype=model.dtype)

    pcm = PCMBuffer(sys.stdin.buffer)
    emit({"type": "ready"})
    log(f"ready (look-ahead {args.lookahead}, {args.threads} threads); waiting for audio")
    if not pcm.ensure(n_first):
        return
    first = features(pcm.get(0, n_first), True)

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
            yield features(pcm.get(start, end), False).input_features
            mel += mel_chunk
            start = mel * hop - n_fft // 2
            pcm.drop_before(start - n_fft)

    # group_tokens=False: RNN-T output must not have repeated tokens merged (a CTC rule).
    streamer = TextIteratorStreamer(processor.tokenizer, skip_special_tokens=True, group_tokens=False)

    def run():
        with torch.inference_mode():
            model.generate(**{**first, "input_features": chunks()}, streamer=streamer)

    worker = threading.Thread(target=run)
    worker.start()
    for text in streamer:
        if text:
            emit({"type": "text", "text": text})
    worker.join()


if __name__ == "__main__":
    try:
        main()
    except (KeyboardInterrupt, BrokenPipeError):
        pass
