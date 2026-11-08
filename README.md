# farhang

Listen to Iranian public radio from the terminal, with a **live, streaming
Persian transcript and translation**.

```
16:59:22  فرهنگ قطار زمان این هفته مرا به دل کوهستان‌های مازندران برد به روستای یوش …
          This week, the "Time Train" took me to the mountains of Mazandaran, to the village of Yush, …
16:59:53  شاعری که از یوش برخاست در برابر عادت‌های ادبی زمان ایستاد و نامش را در تاریخ شعر فارسی موندگار
          He was a poet who stood against the literary conventions of his time, and his name has become a lasting part of the history of Persian poetry.
```

The Persian appears word by word, about a second after it is spoken. Each
phrase is translated as soon as the speaker pauses. Everything runs locally,
on the CPU, with open models.

It works with every station of IRIB's radio portal, [radio.iranseda.ir](https://radio.iranseda.ir):
Radio Farhang (culture) by default, plus Payam, Javan, Goftogu, Khabar, and the
others.

## Quick start

```sh
go install github.com/alexisbcz/farhang@latest

ollama pull translategemma:4b      # translation model, once

farhang -translate English         # listen to Radio Farhang, with Persian + English
```

The first run downloads the speech model (about 2.4 GB) and its Python
dependencies. This happens automatically, through `uv`.

## Usage

```sh
farhang                                   # just listen
farhang -translate English                # Persian transcript + English, word by word
farhang -translate French -fa=false       # French only
farhang -transcribe                       # Persian transcript only
farhang -translate English -log radio.md  # also keep a Markdown transcript
farhang -player none -translate English   # transcript without audio
farhang -lookahead 13 -translate English  # more accurate, ~1 s more latency
farhang -list                             # station ids
farhang -ch 29 -translate English         # Radio Goftogu (talk)
farhang -o farhang.aac                    # record the stream
```

| Flag | Default | |
|---|---|---|
| `-ch` | `16` | station id on radio.iranseda.ir (`-list` shows them) |
| `-translate` | | target language, e.g. `English`, `French`, `German` |
| `-transcribe` | off | Persian transcript only (implied by `-translate`) |
| `-fa` | `true` | show the Persian transcript |
| `-lookahead` | `3` | recognizer look-ahead: `3` = 0.32 s, `13` = 1.12 s (more accurate) |
| `-asr-threads` | `4` | CPU threads for speech recognition |
| `-ollama-model` | `translategemma:4b` | any Ollama model works; TranslateGemma gets its own prompt |
| `-pause` | `1.5s` | silence that ends a phrase |
| `-min-words` / `-max-words` | `8` / `25` | phrase length bounds |
| `-log` | | append the transcript and translations to a Markdown file |
| `-player` | `mpv` | `mpv`, `ffplay` or `none` |
| `-o` | | also record the raw AAC stream |
| `-url` | | play an HLS playlist directly, skipping the station lookup |

## How it works

```
radio.iranseda.ir ──HLS──▶ farhang (Go) ──AAC──▶ mpv (audio)
                                 │
                                 └─AAC─▶ ffmpeg ─16 kHz PCM─▶ Nemotron streaming ASR (Python, via uv)
                                                                   │ words, as they are recognised
                                 ◀───────────────────────────────── 
                                 │ phrase (cut on pauses)
                                 └────────▶ Ollama: TranslateGemma ──▶ translation
```

- **Stream.** The program finds the station's live HLS stream on the official
  player page, follows the playlist, and plays the AAC audio.
- **Speech recognition.** It uses [Nemotron ASR Streaming, Persian](https://huggingface.co/mehdi-hf/nemotron-asr-streaming-farsi):
  NVIDIA's cache-aware streaming FastConformer RNN-T (600M parameters),
  fine-tuned on 1,181 hours of Persian. Audio is fed continuously and words
  come back as they are decoded. The helper script is embedded in the binary,
  and `uv` installs its dependencies on first run.
- **Translation.** Words are grouped into phrases, cut on pauses, and each
  phrase is translated by [TranslateGemma](https://ollama.com/library/translategemma)
  through Ollama. Translations are printed above the phrase being
  transcribed.

### Why this model

I compared speech recognizers on 40 FLEURS Persian utterances with human
transcripts, on a Ryzen 9 6900HX CPU. WER is the share of words wrong; RTF
is processing time divided by audio time.

| Model | WER | RTF |
|---|---|---|
| **Nemotron streaming Persian, 1.12 s look-ahead** | **12.5%** | 0.067 |
| Nemotron streaming Persian, 0.32 s look-ahead | 14.5% | 0.065 |
| Vosk small fa 0.42 | 14.9% | 0.073 |
| Whisper large-v3-turbo (whisper.cpp), 20 s chunks | 32.3% | 0.55 |

Nemotron makes about a third as many errors as Whisper large-v3-turbo, runs
roughly 15× faster than real time on a laptop CPU, and was built for
streaming. Its model card says FLEURS was not in its training data.

## Requirements

- Go 1.26+ (to build)
- [`uv`](https://docs.astral.sh/uv/), which runs the speech recognizer and installs its dependencies
- `ffmpeg`, and `mpv` or `ffplay` for audio
- [Ollama](https://ollama.com) for translation, with `ollama pull translategemma:4b`

The program runs on the CPU. On a Ryzen 9 6900HX, speech recognition uses
about 10% of real time, and translating a phrase takes a few seconds.

## Limits

- Speech recognition makes mistakes, above all with names, and the
  translation inherits them. The model outputs no punctuation.
- Phrases are cut on pauses. A cut can split a sentence or a number (for
  example a year read out as words) and garble its translation. Raise
  `-min-words` or `-pause` for longer phrases.
- Music produces little or no text.
- IRIB can change its CDN or player page at any time. Pass `-url` to give a
  playlist URL directly.

## Licenses

The code is under the MIT License (see `LICENSE`).

The models have their own terms: Nemotron ASR Streaming Persian is under
[OpenMDW-1.1](https://openmdw.ai/license/1-1/), and TranslateGemma under the
[Gemma terms of use](https://ai.google.dev/gemma/terms). The broadcasts belong
to IRIB.
