package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const playbackSegmentSeconds = 30

var segmentBuildLocks sync.Map

func lockPlaybackSegment(path string) func() {
	value, _ := segmentBuildLocks.LoadOrStore(path, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

func segmentPath(name string, offset, duration int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("playback-segment-v4:%s:%d:%d", name, offset, duration)))
	return filepath.Join(playbackCacheDir, "segment-"+hex.EncodeToString(sum[:])+".mp4")
}

func recordingLength(name string) (int, bool) {
	start, end, ok := playbackTimes(name)
	if !ok {
		return 0, false
	}
	return int(end.Sub(start).Seconds()), true
}

func servePlaybackSegment(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("file")
	length, ok := recordingLength(name)
	offset, offsetErr := strconv.Atoi(r.URL.Query().Get("offset"))
	duration, durationErr := strconv.Atoi(r.URL.Query().Get("duration"))
	if !ok || offsetErr != nil || durationErr != nil || offset < 0 || duration < 1 || duration > playbackSegmentSeconds || offset+duration > length {
		jsonResponse(w, 400, map[string]string{"error": "invalid playback segment"})
		return
	}
	path := segmentPath(name, offset, duration)
	unlock := lockPlaybackSegment(path)
	defer unlock()
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		part := path + ".part"
		_ = os.Remove(part)
		err := buildPlaybackSegment(r.Context(), name, offset, duration, part)
		if err == nil {
			err = os.Rename(part, path)
		}
		if err != nil {
			_ = os.Remove(part)
			jsonResponse(w, 502, map[string]string{"error": err.Error()})
			return
		}
	}
	file, err := os.Open(path)
	if err != nil {
		jsonResponse(w, 404, map[string]string{"error": "playback segment not found"})
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		jsonResponse(w, 500, map[string]string{"error": "could not read playback segment"})
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, "segment.mp4", info.ModTime(), file)
}

func buildPlaybackSegment(ctx context.Context, name string, offset, duration int, output string) error {
	fileStart, fileEnd, ok := playbackTimes(name)
	if !ok {
		return fmt.Errorf("recording time range is unavailable")
	}
	rangeStart := fileStart.Add(time.Duration(offset) * time.Second)
	rangeEnd := rangeStart.Add(time.Duration(duration) * time.Second)
	if rangeEnd.After(fileEnd) {
		rangeEnd = fileEnd
	}
	client, err := dialDVRIP(nvrHost, nvrUsername, nvrPassword)
	if err != nil {
		return err
	}
	defer client.close()
	stream, err := client.openRecordingRange(name, rangeStart, rangeEnd)
	if err != nil {
		return err
	}
	defer stream.Close()
	go func() { <-ctx.Done(); _ = stream.Close() }()
	fps, prefix, ended, err := stream.ProbeVideo()
	if err != nil {
		return err
	}
	fpsText := strconv.Itoa(fps)
	// Every MP4 is an independent browser resource, so its media timeline must
	// begin at zero. Raw DVR H.264 timestamps are unreliable, therefore derive
	// stable timestamps from frame order and the recorder's frame-rate metadata.
	setts := fmt.Sprintf("setts=pts=(N/%d)/TB:dts=(N/%d)/TB:duration=1/(%d*TB)", fps, fps, fps)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-hide_banner", "-loglevel", "warning", "-f", "h264", "-framerate", fpsText, "-i", "pipe:0", "-map", "0:v:0", "-an", "-t", strconv.Itoa(duration), "-c:v", "copy", "-bsf:v", setts, "-r", fpsText, "-movflags", "+faststart", "-f", "mp4", output)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	writeErr := stream.WriteRemainingTo(newH264SyncWriter(stdin), prefix, ended)
	_ = stdin.Close()
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if writeErr != nil && writeErr != io.EOF {
		// FFmpeg intentionally closes the pipe once the requested duration is full.
		if _, statErr := os.Stat(output); statErr != nil {
			return writeErr
		}
	}
	if waitErr != nil {
		return fmt.Errorf("FFmpeg segment: %s", stderr.String())
	}
	info, err := os.Stat(output)
	if err != nil || info.Size() == 0 {
		return fmt.Errorf("recorder returned an empty playback segment")
	}
	return nil
}
