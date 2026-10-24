package media

import "strings"

// playbackInput keeps private loopback capabilities off an inherited HTTP
// proxy. Local paths retain the existing ffmpeg/ffprobe invocation.
func playbackInput(path string) []string {
	if strings.HasPrefix(path, "http://127.0.0.1:") {
		return []string{"-http_proxy", ""}
	}
	return nil
}
