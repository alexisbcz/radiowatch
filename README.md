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

It covers three countries:

- **Iran**: all ~60 national and provincial stations of IRIB, the state
  broadcaster.
- **Russia**: the main state news stations (Vesti FM, Radio Rossii, Radio
  Sputnik, the Defence Ministry's Radio Zvezda) and two stations in Crimea.
- **China**: the news channels of China National Radio, CMG Global News Radio,
  the stations aimed at Taiwan, and Beijing News Radio.

Adding a country means adding a station list and checking that the speech
model covers its language.

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

## Russian and Chinese state radio

The same pipeline works on Russian and Mandarin, with a multilingual model that
also restores punctuation. Two excerpts from **November 8, 2026**, raw
speech-recognition output with an English translation.

**Vesti FM** (`ru:vesti`, VGTRK, Russian state radio), on a shooting in Poland:

> **«Он был одет военный камуфляж в бронежилет и имел при себе длинноствольное оружие военного типа, а в автомобиле обнаружили и дополнительные единицы обнестрельного оружия и даже большое количество боеприпасов. И как раз после этого министр обороны Польши косеня Каммиш, он специально опроверг предположение, что стрелявший был военнослужащим»**
>
> "He was dressed in military camouflage and a bulletproof vest and carried a military-style long-barrelled weapon, and in the car they also found more firearms and even a large amount of ammunition. Right after that, **Poland's defence minister** specifically denied the suggestion that the shooter was a soldier."

The recognizer turned the minister's name, Kosiniak-Kamysz, into «косеня
Каммиш»: names remain the weak point.

**CMG Global News Radio** (`cn:global`, Chinese state radio), relaying Israeli
and Iranian claims about the war with Iran:

> **«以色列国防部长卡词十一月八号称, 以色列对伊朗的任务尚未完成。 伊朗试图重启获取核武器的进程, 以方将对威胁采取先发致人的行动。 … 伊朗伊斯兰革命卫队海军八号表示, 又有一艘游轮试图穿越霍尔穆斯海峡为经伊朗授权的航线发生爆炸, 并引发剧列火灾。»**
>
> "**Israeli Defence Minister Katz** said on November 8 that Israel's mission against Iran is not finished. Iran is trying to restart the process of acquiring nuclear weapons, and Israel will act pre-emptively against the threat. … The **IRGC Navy** said on the eighth that **another tanker trying to cross the Strait of Hormuz** on a route not authorised by Iran **exploded and caught fire**."

That second sentence is the kind of item radiowatch exists for, and this
excerpt shows its typical errors. The recognizer heard 游轮 ("cruise ship")
for 油轮 ("oil tanker") and 为 for 未 ("not"), and the live translator turned
Katz into "Ehud Barak." Every item keeps its raw transcript so that a reader
can catch this.

---

## Quick start

```sh
go install github.com/alexisbcz/radiowatch@latest
ollama pull gemma3:4b             # news extraction
ollama pull translategemma:4b     # live translation (listen mode)

radiowatch stations                            # what can be monitored
radiowatch listen -translate English           # Radio Khabar, live, word by word
radiowatch listen -ch ru:vesti -translate English
radiowatch listen -ch cn:global -translate English
radiowatch monitor                             # Khabar + Goftogu + Radio Iran, with dashboard
radiowatch monitor -stations ir:26,ru:vesti,cn:cnr1
```

The first run downloads each speech model it needs, about 2.4 GB each, with
its Python dependencies, automatically, through `uv`. Persian uses one model,
and Russian and Chinese share the other. The monitor also downloads the GeoNames
gazetteers for the countries it covers (2 MB to 30 MB each).

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
  national and provincial station. The IRIB provider finds each live stream,
  retries the flaky player page, and reconnects to a fresh content delivery
  network (CDN) edge when a stream stalls. Russian and Chinese stations are
  fixed lists of verified Icecast and HLS streams, which ffmpeg reads directly.
- **Speech recognition** uses NVIDIA's Nemotron ASR Streaming, a 600M-parameter
  cache-aware streaming RNN-T (recurrent neural network transducer):
  - Persian: a [Persian fine-tune](https://huggingface.co/mehdi-hf/nemotron-asr-streaming-farsi)
  - Russian, Mandarin, and the other supported languages: the
    [multilingual base model](https://huggingface.co/nvidia/nemotron-3.5-asr-streaming-0.6b),
    which also restores punctuation

  One Python helper process (embedded in the binary, run by `uv`) serves every
  station over a local socket. Each model loads once, so adding a station
  doesn't add a copy of a model.
- **Geocoding** uses the GeoNames dumps of each covered country and of the
  places its radio talks about most, such as Ukraine for Russian stations and
  Taiwan for Chinese ones. A built-in table adds seas, straits, nuclear sites,
  and countries.
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
  three simultaneous streams ran at live speed: three Persian stations on one
  model (about 2.8 GB of RAM), or Radio Khabar, Vesti FM and CNR-1 together on
  both models (about 5.3 GB).
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

**Russia**

- `ru:vesti` **Vesti FM**: rolling news from VGTRK, the state broadcaster.
- `ru:rossii` **Radio Rossii**: VGTRK's national station, with regional
  bulletins.
- `ru:sputnik` **Radio Sputnik**: Rossiya Segodnya, the state agency behind RIA
  Novosti.
- `ru:zvezda` **Radio Zvezda**: the Defence Ministry's broadcaster, for the
  military's own framing.
- `ru:krym` **Radio Krym** and `ru:sevastopol` **Govorit Sevastopol**: stations in Crimea,
  where drone and missile strikes on the peninsula are reported locally.
- `ru:mayak` (talk) and `ru:bfm` (Business FM, economy) complete the list.

**China**

- `cn:cnr1` **CNR-1 Voice of China**: the news channel of China National Radio, the
  closest equivalent to a national bulletin.
- `cn:global` **CMG Global News Radio**: international news, relaying foreign
  claims, as in the excerpt earlier.
- `cn:zhonghua` **Voice of Zhonghua** and `cn:huaxia` **Voice of Huaxia**: the
  stations aimed at Taiwan, for the cross-strait message.
- `cn:cnr2` (economy) and `cn:beijing` (Beijing News Radio) complete the list.

## Reliability, and what this isn't

- **Everything is a claim by the station.** IRIB, VGTRK, China Media Group and
  the others are state broadcasters. Their output is propaganda as much as
  news, and radiowatch labels every item as an unverified claim and names its
  source. The archive is still valuable for that reason: it records what each
  state told its public, and when.
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
  resolution). The multilingual model already covers about 30 languages,
  including Arabic, Turkish, Ukrainian, and Korean. North Korean, Belarusian and
  Arabic state radio are natural next steps.
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

- Nemotron ASR Streaming, Persian fine-tune and multilingual base: [OpenMDW-1.1](https://openmdw.ai/license/1-1/)
- Gemma and TranslateGemma: [Gemma terms of use](https://ai.google.dev/gemma/terms)
- Place names: [GeoNames](https://www.geonames.org), CC BY 4.0

The broadcasts belong to their broadcasters. Transcripts and extracted items are for
research and monitoring.
