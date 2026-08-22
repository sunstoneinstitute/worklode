// Package ffmpeg is the one place in the binary that shells out to ffmpeg,
// for the same reason internal/gitexec owns every git subprocess: the
// argument policy, the time budget and the error shape live in one auditable
// spot.
//
// One operation needs it. An embedded <video> with no poster is a black
// rectangle until it is played, which is a poor answer to "show me the bug",
// so the first frame of an uploaded video is extracted and stored as a poster
// image (spec 021 §5, resolving Q021.2).
//
// The dependency is optional at runtime. A server built from source on a
// machine with no ffmpeg stores the video and serves it without a poster
// rather than refusing the upload — which is why ErrUnavailable is a distinct
// error a caller can shrug at, and not a boot-time check.
package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ErrUnavailable reports that no ffmpeg binary is on PATH. Callers treat it
// as "this deployment has no posters", never as an upload failure.
var ErrUnavailable = errors.New("ffmpeg is not installed")

// PosterMediaType is what Poster returns and what the caller stores those
// bytes under. It is also what http.DetectContentType sniffs them as, so a
// poster blob is indistinguishable from a JPEG someone uploaded by hand.
const PosterMediaType = "image/jpeg"

// posterTimeout bounds one extraction. Decoding a single frame of a
// well-formed file is milliseconds; the budget exists for the file that is
// not well-formed, where ffmpeg can probe for a long time before it gives up.
const posterTimeout = 20 * time.Second

// maxPosterBytes caps what is read back. One JPEG frame of even a 4K video is
// far under this; more than this means ffmpeg is emitting something other
// than the one frame that was asked for, and a truncated JPEG is worse than
// no poster.
const maxPosterBytes = 16 << 20

// maxStderrBytes caps how much of ffmpeg's diagnostics reach the error, which
// reaches a log line.
const maxStderrBytes = 4 << 10

// Poster returns the first video frame of the file at path, JPEG-encoded.
//
// path is a local file rather than a reader on purpose: the caller already
// has the upload spooled to disk, and ffmpeg needs to seek to read a
// moov-atom-at-the-end MP4 at all. Piping one in would either fail on those
// files or buffer 100 MiB to make them work.
func Poster(ctx context.Context, path string) ([]byte, error) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, posterTimeout)
	defer cancel()

	// -map 0:v:0 pins the first video stream, so a file whose first stream is
	// audio or an embedded cover image does not choose the poster for us.
	// -frames:v 1 stops after that frame, which is what makes this cost the
	// same on a 100 MiB screen recording as on a 1 MiB one.
	cmd := exec.CommandContext(ctx, bin,
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", path,
		"-map", "0:v:0", "-frames:v", "1", "-an",
		"-q:v", "4", "-f", "mjpeg", "pipe:1",
	)
	out := &cappedBuffer{limit: maxPosterBytes}
	var stderr bytes.Buffer
	cmd.Stdout = out
	cmd.Stderr = &truncBuffer{buf: &stderr, limit: maxStderrBytes}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg poster: %w%s", err, detail(stderr.String()))
	}
	if out.buf.Len() == 0 {
		// A zero exit with no output is what a file with no decodable video
		// stream looks like.
		return nil, errors.New("ffmpeg poster: no frame decoded")
	}
	return out.buf.Bytes(), nil
}

// detail renders ffmpeg's own complaint as an error suffix, or nothing when
// it had none.
func detail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return ": " + strings.ReplaceAll(s, "\n", "; ")
}

// cappedBuffer fails the write — and so the command — past limit, rather than
// silently truncating a JPEG into something no decoder will open.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (w *cappedBuffer) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.limit {
		return 0, errors.New("output over the poster size cap")
	}
	return w.buf.Write(p)
}

// truncBuffer keeps the first limit bytes and drops the rest. Diagnostics are
// a nice-to-have, so overrunning them must not fail the command the way
// cappedBuffer deliberately does.
type truncBuffer struct {
	buf   *bytes.Buffer
	limit int
}

func (w *truncBuffer) Write(p []byte) (int, error) {
	if room := w.limit - w.buf.Len(); room > 0 {
		w.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}
