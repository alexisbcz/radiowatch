# radiowatch

**Live transcription, translation, and news extraction from state radio.**

radiowatch listens to radio stations around the clock, transcribes them as they
speak, and turns what they broadcast into structured, geolocated, attributed
news items in JSON, GeoJSON, and RSS (Really Simple Syndication) feeds that a
dashboard such as
[World Monitor](https://github.com/koala73/worldmonitor) can consume.

The idea: a country's own state radio sometimes reports things before anyone
else. That includes a strike on one of its cities, an explosion, air-defence
activity, a casualty count, or a threat issued by an official. A provincial
station can mention a local incident hours before it reaches the wire
services, if it ever does. radiowatch makes that audio searchable and
machine-readable in real time.

It starts with **Iran**: all ~60 national and provincial stations of IRIB, the
state broadcaster. The design leaves room for Russian, Chinese, and other radio
next.

Everything runs locally, on a laptop CPU, with open models.

---

## What you hear on Iranian state radio

These are real excerpts, recorded by radiowatch on **November 8, 2026, around
20:00 Tehran time**, from **Radio Khabar** (IRIB's news channel, `ir:26`) and
**Radio Goftogu** (its political talk channel, `ir:29`). That evening the two
channels were simulcasting the same programme. The Persian is the raw
speech-recognition output, recognition errors included, and the English is a
translation.

In this segment the presenters read out and comment on an American analysis of
US options against Iran. What follows is what IRIB broadcast, and nobody has
verified it.

> **«بر اساس گزارش سرویس تحقیقات کنگره ارتش ایالات متحده از آغاز جنگ تاکنون هشتاد و یک فروند هواگرد را از دست داده یا دچار آسیب کرده است از جمله تعدادی که پس از قرار گرفتن در معرض آتش نیروهای ایرانی سقوط کردن بیست و یک نفر از پرسنل نظامی آمریکا در این جنگ کشته شدند»**
>
> "According to a Congressional Research Service report, since the start of the war the US military has lost or had damaged **eighty-one aircraft**, including several that crashed after coming under fire from Iranian forces. **Twenty-one American military personnel** have been killed in this war."

> **«مقامات ایرانی هشدار دادند که هرگونه کارزار جدید منجر به واکنشی بی‌سابقه خواهد شد که می‌تواند شامل هدف قرار دادن کابل‌های زیردریایی در تنگه هرمز و این روز‌ها در باب المندب نیز بشود ایران تهدید کرده است که ممکن است به پایگاه مورد استفاده نیروهای آمریکایی در اروپا نیز حمله کند»**
>
> "Iranian officials have warned that any new campaign will lead to an unprecedented response, which could include **targeting the undersea cables in the Strait of Hormuz** and, these days, in **Bab el-Mandeb** too. Iran has threatened that it may also **attack bases used by American forces in Europe**."

> **«محسن رضایی از مقامات ارشد امنیتی ایران در هفته‌های اخیر نیز تهدید کرده است که طرح‌های عملیاتی جدیدی را به کار خواهند گرفت و جبهه‌های تازه ای خواهند کشوند و غافلگیری‌های بزرگتری را رقم خواهند زد در ماه سپتامبر نیز ایران مدعی شد که موشک‌های جدید ضد کشتی خود را دریای عمان به کار گرفته است»**
>
> "**Mohsen Rezaei**, a senior Iranian security official, has also threatened in recent weeks that they will deploy new operational plans, **open new fronts**, and spring bigger surprises. In September, Iran also claimed to have used its **new anti-ship missiles in the Sea of Oman**."

Earlier the same evening, the same programme on US strike scenarios against
Iran's deepest nuclear site:

> **«ایالات متحده می‌تواند حملات جدیدی رو علیه اهداف هسته‌ای انجام دهد از آنجا که تصور می‌شود ذخایر اورانیوم با غنای بالای ایران در زیر آوارهای بمباران‌های پیشین آمریکا مدفون شده باشد … محتمل ترین هدف ما کوه کلنگ است مکانی که تاسیسات در عمق بسیار زیاد در آن قرار دارند و تاکنون نیز مورد حمله واقع نشده»**
>
> "…the United States could carry out new strikes on nuclear targets, since Iran's **highly enriched uranium stockpiles are believed to be buried under the rubble** of earlier American bombing… the most likely target is **Kuh-e Kolang** [Pickaxe Mountain, next to Natanz], a site whose facilities lie very deep and which **hasn't been attacked so far**."

Within minutes of broadcast, radiowatch turned that audio into items like this
one (abridged), served on `/api/events`, `/feed.xml` and `/api/events.geojson`:

```json
{
  "title": "Iran Warns of Unprecedented Response to New Conflict",
  "summary": "Mohsen Rezai stated that any new conflict would trigger an unprecedented response, potentially including attacks on underwater cables in the Strait of Hormuz and Bab-el-Mandeb.",
  "kind": "threat",
  "category": "military",
  "breaking": false,
  "importance": 3,
  "claimedBy": "Mohsen Rezai",
  "quote": "هرگونه کارزار جدید منجر به واکنشی بی‌سابقه خواهد شد که می‌تواند شامل هدف قرار دادن کابل‌های زیردریایی در تنگه هرمز و این روز‌ها در باب المندب بشود",
  "quoteVerified": true,
  "station": { "id": "ir:26", "name": "Radio Khabar (news)", "owner": "IRIB (Iranian state broadcaster)" },
  "reliability": "Claim broadcast by state media; automatically transcribed and extracted; not independently verified."
}
```

This example shows the strengths and the limits. The quote is real (radiowatch
checks every quote against the transcript), and the item is correctly
classified as a *threat* rather than an event. But the small local model
attributed it to Mohsen Rezaei, while on air it came from "Iranian officials."
The broadcast quotes Rezaei for a different threat a sentence later. Treat extracted items
as leads to check against the transcript, which is always archived alongside.

---

## Quick start

```sh
go install github.com/alexisbcz/radiowatch@latest
ollama pull gemma3:4b             # news extraction
ollama pull translategemma:4b     # live translation (listen mode)

radiowatch stations               # what can be monitored
radiowatch listen -translate English          # Radio Khabar, live, word by word
radiowatch monitor                            # Khabar + Goftogu + Radio Iran, with dashboard
```

The first run downloads the speech model (about 2.4 GB) and its Python
dependencies, automatically, through `uv`. It also downloads the GeoNames
gazetteer for Iran (about 14 MB).

### `radiowatch listen`: one station, live

Plays a station and prints the Persian as it's spoken, about a second behind
the audio, and translates each phrase when the speaker pauses.

```sh
radiowatch listen -ch ir:29 -translate English        # Radio Goftogu (talk)
radiowatch listen -translate French -fa=false         # French only
radiowatch listen -translate English -log notes.md    # keep a Markdown transcript
radiowatch listen -player none -transcribe            # transcript only, no audio
```

### `radiowatch monitor`: many stations, headless

```sh
radiowatch monitor -stations ir:26,ir:29,ir:11,ir:502,ir:514
```

For every station, in parallel:

1. **Transcribe.** The recognizer transcribes the audio continuously, and
   radiowatch archives the transcripts as JSONL per station and day.
2. **Extract.** Every 5 minutes (`-window`), the transcript window goes to a
   large language model (LLM), which returns the newsworthy items in English. Each item gets a
   `kind`:
   - `event_report`: something the station says happened
   - `official_statement`
   - `threat`
   - `analysis`
   - `foreign_media_citation`

   Each item also gets a category, an importance score, who made the claim, the
   places involved, and the verbatim Persian quote it rests on.
3. **Verify the quote.** radiowatch matches each quote against the transcript.
   An item whose quote it can't find loses importance and can't be breaking.
4. **Geolocate.** Places are geocoded with GeoNames (Persian names included)
   and a table of strategic sites: straits, nuclear sites, regional countries.
5. **Merge.** Repeats on the same station become extra mentions of one item.
   The same story on another station goes into `alsoOn`.
6. **Skip simulcasts.** When two channels carry the same programme (IRIB often
   relays one programme on several channels), radiowatch extracts it only once.
7. **Publish.** radiowatch serves the results over HTTP and pushes breaking or
   important items to a webhook.

| Endpoint | |
|---|---|
| `/` | live dashboard: extracted items, station status, scrolling transcript |
| `/api/events` | events as JSON. Filters: `since=2h`, `station=ir:26`, `kind=event_report,threat`, `breaking=1`, `min_importance=3`, `limit` |
| `/api/events.geojson` | the same, as GeoJSON points for a map layer |
| `/feed.xml`, `/feed/breaking.xml` | RSS 2.0 |
| `/api/stations` | connection status, words transcribed, items per station |
| `/api/transcript?station=ir:26&minutes=10` | the live transcript |

| Flag | Default | |
|---|---|---|
| `-stations` | `ir:26,ir:29,ir:11` | stations to monitor |
| `-window` | `5m` | transcript window per extraction |
| `-llm-url` | `http://localhost:11434/v1` | any OpenAI-compatible endpoint (key in `RADIOWATCH_LLM_KEY`) |
| `-llm-model` | `gemma3:4b` | extraction model |
| `-webhook` / `-webhook-min` | / `4` | POST new events of at least this importance (breaking always) |
| `-http` | `127.0.0.1:8787` | dashboard and feeds |
| `-data` | `radiowatch-data` | transcripts and `events.jsonl` |
| `-lookahead` | `13` | recognizer look-ahead (1.12 s, most accurate) |

### Use with World Monitor

World Monitor builds its panels from RSS feeds and geolocated event layers, and
labels every headline with its source, propaganda risk, and corroboration.
radiowatch provides the matching pieces:

- **News panel.** Add `http://<host>:8787/feed.xml` (or `/feed/breaking.xml`)
  as a custom feed. Each item carries the station, the `kind`, who made the
  claim, the original quote, and a reliability note.
- **Map layer.** `/api/events.geojson` gives points with title, kind,
  category, importance, and station.
- **Alerts.** `-webhook` pushes each breaking item as JSON the moment
  radiowatch extracts it.

Show everything from IRIB as **single-publisher, high propaganda risk**. Items carried by several IRIB channels (`alsoOn`) are the same
publisher, not independent corroboration.

---

## How it works

```
                 ┌── mpv (listen mode)
radio (HLS) ─────┤
                 └── ffmpeg ─ 16 kHz PCM ─▶ Nemotron streaming ASR ─▶ words, live
                                            (one model, many stations)        │
                         ┌────────────────────────────────────────────────────┘
                         ▼
             transcript windows ─▶ LLM (JSON schema) ─▶ quote check ─▶ geocoding ─▶ dedupe ─▶ events
                         │                                                                   │
                  archive (JSONL)                          dashboard · JSON · GeoJSON · RSS · webhook
```

- **Sources** are providers. IRIB's portal, radio.iranseda.ir, lists every
  national and provincial station. The provider finds each live HLS stream,
  retries the flaky player page, and reconnects to a fresh content delivery network (CDN) edge when a
  stream stalls.
- **Speech recognition** uses [Nemotron ASR Streaming, Persian](https://huggingface.co/mehdi-hf/nemotron-asr-streaming-farsi),
  a 600M-parameter cache-aware streaming RNN-T (recurrent neural network transducer). One Python helper process
  (embedded in the binary, run by `uv`) serves every station over a local
  socket, so adding a station doesn't add a copy of the model.
- **Extraction** uses any OpenAI-compatible endpoint with a strict JSON
  schema. The prompt is in [`prompts/extract.md`](prompts/extract.md). It
  makes the model separate what the station says *happened* from analysis,
  threats and retold foreign articles, which make up most of a talk show.
- **Translation** in listen mode uses [TranslateGemma](https://ollama.com/library/translategemma)
  through Ollama.

### Why this speech model

The benchmark compared speech recognizers on 40 FLEURS Persian utterances with
human transcripts, on a Ryzen 9 6900HX CPU. WER (word error rate) is the share
of words wrong, and RTF (real-time factor) is processing time divided by audio
time.

| Model | WER | RTF |
|---|---|---|
| **Nemotron streaming Persian, 1.12 s look-ahead** | **12.5%** | 0.067 |
| Nemotron streaming Persian, 0.32 s look-ahead | 14.5% | 0.065 |
| Vosk small fa 0.42 | 14.9% | 0.073 |
| Whisper large-v3-turbo (whisper.cpp), 20 s chunks | 32.3% | 0.55 |

### Performance

On a laptop CPU (Ryzen 9 6900HX, no GPU):

- **Speech recognition** keeps up with several stations at once. In testing,
  three simultaneous streams ran at live speed with one model in memory
  (about 2.8 GB of RAM).
- **Extraction** is the bottleneck. gemma3:4b takes about 2 minutes per
  5-minute window, so two or three stations is the comfortable limit on a CPU.
  For more stations or better extraction, point `-llm-url` at a GPU machine or
  a hosted model.

## Which stations to watch

`radiowatch stations` lists them all. News and talk stations come first.

- `ir:26` **Radio Khabar** is IRIB's news channel, and `ir:29` **Radio Goftogu**
  is its political talk channel. Both are the core of any watch.
- `ir:11` **Radio Iran** carries the main hourly bulletins between other
  programmes.
- **Provincial stations** (`ir:501`–`ir:538`) are where local incidents
  surface first: strikes, explosions, air-defence activity, damage, casualties.
  Pick by geography:
  - `ir:502` Khuzestan (oil, border with Iraq)
  - `ir:514` Persian Gulf (Hormozgan, Strait of Hormuz)
  - `ir:523` Bushehr (nuclear plant)
  - `ir:525` Isfahan (nuclear sites)
  - `ir:511` Kermanshah (western border)
  - `ir:528` Razavi Khorasan

## Reliability, and what this isn't

- **Everything is a claim by the station.** IRIB is Iran's state broadcaster.
  Its output is propaganda as much as news, and radiowatch labels every item as an
  unverified claim and names its source. The archive is still valuable for that
  reason: it records what the Iranian state told its public, and when.
- **Errors pile up.** Speech recognition errs, especially on names, and the
  model outputs no punctuation. A small local LLM misclassifies and
  misattributes, as the preceding example shows. Verified quotes and the archived
  transcript let a human check every item in seconds.
- **Talk shows are mostly analysis.** Programmes like the one quoted earlier retell
  foreign articles and discuss scenarios. The extractor labels them `analysis`
  or `foreign_media_citation`. Only `event_report` and `official_statement`
  items can be breaking.

## Roadmap

- **More countries.** A new country needs a `Provider` (station list and stream
  resolution) and a speech backend for its language. Russian state radio
  (Radio Rossii, Vesti FM) and Chinese national radio (CNR) are next. Whisper
  or language-specific streaming models can serve the languages Nemotron
  Persian doesn't.
- **Better extraction.** Run a larger model on a GPU or a hosted model, and
  add a second pass to verify `breaking` items before alerting.
- **Cross-source correlation.** Compare items with other sources (wire
  services, World Monitor's own feeds) to spot what state radio reported
  first.

## Requirements

- Go 1.26+ (to build)
- [`uv`](https://docs.astral.sh/uv/), which runs the speech recognizer and installs its dependencies
- `ffmpeg`, plus `mpv` or `ffplay` for listen mode
- An OpenAI-compatible endpoint for extraction and translation, such as [Ollama](https://ollama.com)

## Licenses

The code is under the MIT License (see `LICENSE`). The models and data have
their own terms:

- Nemotron ASR Streaming Persian: [OpenMDW-1.1](https://openmdw.ai/license/1-1/)
- Gemma and TranslateGemma: [Gemma terms of use](https://ai.google.dev/gemma/terms)
- Place names: [GeoNames](https://www.geonames.org), CC BY 4.0

The broadcasts belong to IRIB. Transcripts and extracted items are for
research and monitoring.
