# Media

What `tele` draws inline, what it plays on its own, and what it hands to an
external program. Settings for all of it live under `photos:` in
[configuration.md](configuration.md#photos).

## Photos

Rendered inline in high quality via the Kitty graphics protocol in kitty,
Ghostty and iTerm2 3.7.0 or newer, with an ANSI block-art fallback everywhere
else. tmux and screen do not pass the protocol through, so images are block art
inside them.

Foot, and other terminals that draw DEC sixel images but not Kitty placements,
get a third mode: `photos.mode: sixel` (picked automatically for `TERM=foot`).
The photo modal is then painted through the sixel path at full terminal
resolution, while the message list keeps block art. That split is not a
shortcut: a sixel is painted at the cursor and owned by the cells it covers, and
the terminal erases it as soon as anything rewrites those cells - which every
scrolling repaint of the list does. The modal holds still, so it can be painted
once, after the frame, over cells that stay blank while it is open.

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
shown as a circle. Press `o` to play in the system player.

## GIFs

An inline static thumbnail with a `GIF` badge. The selected GIF loops silently
in place in Kitty graphics mode. This needs `ffmpeg` - see below.

## Audio (music)

Performer, title and duration. Other media types show a labelled placeholder.

## Sending media

Attach an existing file from disk with `u` - photos, videos, voice notes, music,
documents - and confirm the send-as type before sending. Press `u` again to
stage more files: they are sent as one grouped album, with photos and documents
split into separate albums automatically.

## Recording is out of scope

`tele` can _send_ a pre-recorded audio or video file, but it cannot **record**
voice messages or round videos (кружки) in-app, and it does not do real-time
voice or video calls. As a terminal-native, cgo-free client it has no microphone
or camera capture stack. This is an architectural boundary, not a missing
feature on the roadmap.

## Optional dependency: `ffmpeg`

Install `ffmpeg` (with `ffprobe`) on your `PATH` to enable inline GIF playback,
which decodes frames, and to attach duration, dimensions and a thumbnail when
sending videos. It is entirely optional: without it, GIFs stay static and videos
still send, since Telegram generates the preview server-side.
