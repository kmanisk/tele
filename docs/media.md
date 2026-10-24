# Media

What `tele` draws inline, what it plays on its own, and what it hands to an
external program. Settings for all of it live under `photos:` in
[configuration.md](configuration.md#photos).

## Photos

Rendered inline in high quality via the Kitty graphics protocol in kitty,
Ghostty and iTerm2 3.7.0 or newer, with an ANSI block-art fallback everywhere
else. tmux and screen do not pass the protocol through, so images are block art
inside them.

Press `o` to open the full-quality image in an in-app modal viewer, with sender
and timestamp on the border, or `O` to open it in an external viewer.

## Voice messages

An amplitude waveform with duration, and in-app playback on `p` with an animated
playhead.

Playback is cgo-free on every platform: Opus/Ogg is decoded in pure Go, and
audio goes out via `oto` on macOS and Windows, or the PulseAudio/PipeWire
protocol on Linux. On Linux this needs a running PulseAudio or PipeWire server,
which is the desktop default.

## Video and round video (кружки)

An inline thumbnail preview with a `▶` and duration overlay; round notes are
shown as a circle. Press `o` to open a video. In Kitty graphics mode with
`ffmpeg`, it plays silently in the existing modal: space pauses/resumes, the
arrow keys page through an album, `o` switches to an external player, and
escape closes it. In other terminals, including Foot, it opens externally.

Videos with a known file size use progressive playback: the account owner
serves requested Telegram byte ranges through a private, temporary loopback
URL. Playback can start before the entire video arrives, without creating a
complete temporary video file. Container metadata can be fetched from the end
of the file. Incoming `SupportsStreaming` metadata is not required. A small
bounded memory cache avoids repeated reads; this does not fill the disk cache.

On Linux, external playback uses `mpv` when it is on `PATH`, including for `O`.
Seeking in mpv retrieves the required ranges directly. The in-app modal keeps
its existing controls and does not add seeking or audio. Other platforms, or
Linux without mpv, retain full download followed by the system player; an HTTP
URL is never handed to `xdg-open`. Missing size, transport failure during
opening, or player failure also falls back to the complete local file.

Closing the modal or mpv, opening another video or photo, disconnecting the
window, or exiting cancels its stream. Tele owns the mpv process it launches.
Sessions expire after four hours. Pausing applies backpressure, with a small
amount of player/socket read-ahead; it does not save the whole file.

The explicit Download action is separate: it always saves the complete file
to the Downloads directory using the existing downloader, with progress.

## GIFs

An inline static thumbnail with a `GIF` badge. The selected GIF loops silently
in place in Kitty graphics mode. This needs `ffmpeg`, see below.

## Audio (music)

Performer, title and duration. Other media types show a labelled placeholder.

## Sending media

Attach an existing file from disk with `u` (photos, videos, voice notes, music,
documents) and confirm the send-as type before sending. Press `u` again to
stage more files: they are sent as one grouped album, with photos and documents
split into separate albums automatically.

## Recording is out of scope

`tele` can _send_ a pre-recorded audio or video file, but it cannot **record**
voice messages or round videos (кружки) in-app, and it does not do real-time
voice or video calls. As a terminal-native, cgo-free client it has no microphone
or camera capture stack. This is an architectural boundary, not a missing
feature on the roadmap.

## Optional dependency: `ffmpeg`

Install `ffmpeg` (with `ffprobe`) on your `PATH` for in-app video playback,
inline GIF playback, and duration, dimensions and thumbnails when sending
videos. Install `mpv` for progressive external video playback on Linux.
These are optional: without ffmpeg, GIFs stay static and videos still send
(Telegram generates the preview server-side); without mpv, the system player
opens a completed download.
