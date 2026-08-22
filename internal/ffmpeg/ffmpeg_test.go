package ffmpeg_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/ffmpeg"
)

// fakeFFmpeg puts a stub ffmpeg on PATH for the duration of the test and
// returns the file its arguments are recorded in. The real binary is not on
// every developer machine and is not on the CI test runner, so a test that
// needed it would be a test that never runs; the stub is what keeps the
// argument list, the output handling and the failure paths covered
// everywhere. TestPosterWithRealFFmpeg is the round trip, and it skips.
func fakeFFmpeg(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" >" + args + "\n" + script
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", dir)
	return args
}

func recordedArgs(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	return string(b)
}

// TestPosterUnavailable: a deployment with no ffmpeg must be distinguishable
// from a video that failed to decode, because the caller shrugs at one and
// logs the other.
func TestPosterUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := ffmpeg.Poster(context.Background(), "video.mp4")
	if !errors.Is(err, ffmpeg.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// TestPoster covers the success path: stdout is the poster, and the argument
// list asks for exactly one frame of the first video stream of the named
// file.
func TestPoster(t *testing.T) {
	args := fakeFFmpeg(t, `printf '\377\330\377\341ok'`)

	got, err := ffmpeg.Poster(context.Background(), "/spool/lode-blob-123")
	if err != nil {
		t.Fatalf("Poster: %v", err)
	}
	if string(got) != "\xff\xd8\xff\xe1ok" {
		t.Fatalf("poster = %q, want the stub's stdout", got)
	}

	recorded := recordedArgs(t, args)
	for _, want := range []string{"-i\n/spool/lode-blob-123\n", "-map\n0:v:0\n", "-frames:v\n1\n", "-f\nmjpeg\n", "pipe:1\n"} {
		if !strings.Contains(recorded, want) {
			t.Errorf("args %q missing %q", recorded, want)
		}
	}
}

// TestPosterFailureCarriesStderr: the error is what a log line shows an
// operator, so ffmpeg's own complaint has to survive into it.
func TestPosterFailureCarriesStderr(t *testing.T) {
	fakeFFmpeg(t, "echo 'moov atom not found' >&2\nexit 1\n")

	_, err := ffmpeg.Poster(context.Background(), "broken.mp4")
	if err == nil {
		t.Fatal("no error for a failing ffmpeg")
	}
	if errors.Is(err, ffmpeg.ErrUnavailable) {
		t.Fatalf("err = %v, want a decode failure and not ErrUnavailable", err)
	}
	if !strings.Contains(err.Error(), "moov atom not found") {
		t.Fatalf("err = %v, want it to carry ffmpeg's stderr", err)
	}
}

// TestPosterNoFrame: a clean exit with no bytes is what a file with no
// decodable video stream looks like, and storing zero bytes as a poster would
// render as a broken image forever.
func TestPosterNoFrame(t *testing.T) {
	fakeFFmpeg(t, "exit 0\n")

	_, err := ffmpeg.Poster(context.Background(), "audio-only.webm")
	if err == nil {
		t.Fatal("no error for an ffmpeg that produced nothing")
	}
}

// TestPosterWithRealFFmpeg is the round trip, on a machine that has the
// binary the server image ships: synthesise a video, extract frame 0, and
// check the bytes are a JPEG.
func TestPosterWithRealFFmpeg(t *testing.T) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	video := filepath.Join(t.TempDir(), "in.mp4")
	gen := exec.Command(bin, "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=160x120:rate=5:duration=1",
		"-c:v", "mpeg4", "-y", video)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("this ffmpeg cannot synthesise a test video: %v: %s", err, out)
	}

	got, err := ffmpeg.Poster(context.Background(), video)
	if err != nil {
		t.Fatalf("Poster: %v", err)
	}
	if len(got) < 4 || got[0] != 0xff || got[1] != 0xd8 || got[2] != 0xff {
		t.Fatalf("poster of %d bytes does not start with the JPEG magic", len(got))
	}
}
