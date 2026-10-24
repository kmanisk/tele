# Progressive video transport

## Confirmed baseline

Inspected upstream `main` at `6aa6cf990a515be1dba7fd8250af6d1587c03ed2`.
`root_media.go` selects Kitty plus ffmpeg for the in-app video modal; other
terminals use `startDocumentOpen`. Album video parts follow the same choice.

In-app: `saveVideoFileCmd` calls `Surface.SaveMedia(DocFull)`; the attachment
and seat delegate to `mediaFetcher.Save`, which downloads the complete document
into a temporary file. Only then does `videoFileReadyMsg` start ffprobe and
`OpenFrameSource` start ffmpeg. Its bounded channel streams decoded frames from
that completed local file, not progressively retrieved Telegram bytes.

External: `openDocumentCmd` also waits for `SaveMedia(DocFull)` before handing
the completed path to `xdg-open`, `open`, or Windows `start`. Explicit Download
uses the same full-file save operation with the Downloads directory.

`tg/download.go` already writes sequentially to `io.Writer` with bounded memory.
It has no random access API. `mediaFetcher.streamInto` refreshes stale message
file references once, using a per-document cooldown. The IPC surface exposes
paths, not Telegram clients. Seats end on disconnect or account termination.
`DocumentRef` preserves file size and MIME type; incoming video classification
does not retain `SupportsStreaming`. No schema change is needed: ranges also
allow ffmpeg to read container metadata at the end of a file.

## Library evaluation

`github.com/gotd/contrib` v0.25.0 requires Tele's existing gotd v0.161.0.
`tg_io.ChunkSource` supports aligned, precise `upload.getFile` reads and request
contexts. gotd handles file DC migration through its existing authenticated
invoker. CDN support is not requested by this helper.

`partio` aligns reads but streams to EOF without a bounded-range operation or
cache. `http_io` sets a bounded Content-Length but calls that unbounded stream;
it is therefore unsuitable for this gateway. `http_range` is unnecessary when
using Go's tested `http.ServeContent` with a request-scoped section reader.
Source: <https://github.com/gotd/contrib/tree/v0.25.0>.

## Ownership and bounds

Each attachment owns at most one stream. `OpenMediaStream(chatID, msgID, seq)`
returns a sequence ID and loopback URL; `CloseMediaStream(seq)` cancels through
that attachment's sequence. A monotonically increasing sequence prevents late
opens or lost IPC replies from leaking a listener, and a stale close cannot
terminate a newer session. Replacement, detach, account termination and
owner shutdown cancel it. A UI playback context also releases late replies.
Opening is an additive IPC operation; older hosts can refuse it and the UI
falls back to `SaveMedia`.

The source fetches aligned 128 KiB parts, with one Telegram read in flight per
session, and an eight-part LRU (1 MiB retained). Alignment divides Telegram's
1 MiB windows and meets its precise 1 KiB alignment requirement (see
<https://core.telegram.org/api/files#downloading-files>). Cache identity is the
session's immutable document plus offset; a refreshed message changing document
ID or size terminates streaming. A shared cancellable refresh gate reuses fresh
references from concurrent callers and retains the existing cooldown. Each
range has a 15 second deadline, one stale-reference retry or one network retry.

The HTTP listener binds `127.0.0.1:0`; paths contain 256 bits of randomness.
GET, HEAD, single bounded/open-ended/suffix ranges, 200, 206 and 416 use
`http.ServeContent`. Multipart ranges are rejected. Unknown paths return 404
and other methods return 405. Four active handlers and a small socket send
buffer bound extra read-ahead beyond the cache. No gateway request is logged.
No disk writes or permanent worker/service are used. A four-hour deadline is
the abandonment backstop. UI ffprobe/ffmpeg and mpv bypass HTTP proxies for the
private loopback source.

Frame reads happen in Bubble Tea commands, so slow input does not block close
or album paging. Commands carry the exact modal identity to discard stale
answers. ffmpeg and mpv are reaped even if the UI exits before their results
arrive. The current frame geometry, 15 FPS, silent modal and renderer remain.

## Reproducible validation

The focused race tests cover aligned/unaligned reads, EOF, cache
eviction, simulated 2 GiB direct seeks, stale references, replacement, detach,
HTTP semantics, cancellation and UI source ownership. The ffmpeg test creates
a synthetic MP4 with metadata at its end, probes/decodes through the production
gateway, and seeks to 80%. It skips when ffmpeg or ffprobe is unavailable.

```sh
go test -race ./internal/videostream ./internal/core ./internal/tg ./internal/ui ./internal/ipc/...
```

`TELE_MPV_TEST=1 go test -race -v ./internal/videostream` additionally runs a
headless mpv open/seek/quit test, without requiring a graphical player in normal
unit tests. It reports source bytes fetched, not decoded frame bytes. Synthetic
timings measure a local fake source and are not Telegram network benchmarks.
Real-account large-video, memory and network-interruption validation must be
recorded separately; no private media belongs in test fixtures or logs.
