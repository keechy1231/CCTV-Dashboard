package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

const hlsSegmentSeconds = 30

// The recorder has a small DVRIP connection limit and live view already uses
// several slots. Keep archive reads bounded so HLS prefetch cannot exhaust it.
var hlsBuildSlot = make(chan struct{}, 1)

func servePlaybackHLS(w http.ResponseWriter, r *http.Request) {
	channel, channelErr := strconv.Atoi(r.URL.Query().Get("channel"))
	day, dateErr := time.ParseInLocation("2006-01-02", r.URL.Query().Get("date"), time.Local)
	if channelErr != nil || channel < 0 || channel > 31 || dateErr != nil {
		jsonResponse(w, 400, map[string]string{"error": "invalid playback channel or date"})
		return
	}
	dayEnd := day.Add(24 * time.Hour)
	if time.Now().Before(dayEnd) {
		dayEnd = time.Now()
	}
	length := int(dayEnd.Sub(day).Seconds())
	if length < 1 {
		jsonResponse(w, 400, map[string]string{"error": "the selected date is in the future"})
		return
	}
	if r.URL.Query().Get("segment") == "" {
		serveHLSPlaylist(w, channel, day, length)
		return
	}
	index, err := strconv.Atoi(r.URL.Query().Get("segment"))
	offset := index * hlsSegmentSeconds
	if err != nil || index < 0 || offset >= length {
		jsonResponse(w, 400, map[string]string{"error": "invalid playback segment"})
		return
	}
	duration := min(hlsSegmentSeconds, length-offset)
	serveHLSSegment(w, r, channel, day, offset, duration)
}

func serveHLSPlaylist(w http.ResponseWriter, channel int, day time.Time, length int) {
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "private, max-age=60")
	_, _ = fmt.Fprintf(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-INDEPENDENT-SEGMENTS\n", hlsSegmentSeconds)
	encodedDate := url.QueryEscape(day.Format("2006-01-02"))
	for offset, index := 0, 0; offset < length; offset, index = offset+hlsSegmentSeconds, index+1 {
		duration := min(hlsSegmentSeconds, length-offset)
		_, _ = fmt.Fprintf(w, "#EXTINF:%.3f,\n/api/playback/hls?channel=%d&date=%s&segment=%d\n", float64(duration), channel, encodedDate, index)
	}
	_, _ = io.WriteString(w, "#EXT-X-ENDLIST\n")
}

func hlsSegmentPath(channel int, day time.Time, offset, duration int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("playback-by-time-hls-v2:%d:%s:%d:%d", channel, day.Format("2006-01-02"), offset, duration)))
	return filepath.Join(playbackCacheDir, "hls-"+hex.EncodeToString(sum[:])+".ts")
}

func serveHLSSegment(w http.ResponseWriter, r *http.Request, channel int, day time.Time, offset, duration int) {
	path := hlsSegmentPath(channel, day, offset, duration)
	unlock := lockPlaybackSegment(path)
	defer unlock()
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		part := path + ".part"
		_ = os.Remove(part)
		err := buildHLSSegment(r.Context(), channel, day, offset, duration, part)
		if err == nil {
			err = os.Rename(part, path)
		}
		if err != nil {
			_ = os.Remove(part)
			jsonResponse(w, 502, map[string]string{"error": err.Error()})
			return
		}
	}
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, path)
}

func buildHLSSegment(ctx context.Context, channel int, day time.Time, offset, duration int, output string) error {
	select {
	case hlsBuildSlot <- struct{}{}:
		defer func() { <-hlsBuildSlot }()
	case <-ctx.Done():
		return ctx.Err()
	}
	rangeStart := day.Add(time.Duration(offset) * time.Second)
	rangeEnd := rangeStart.Add(time.Duration(duration) * time.Second)
	client, err := dialDVRIP(nvrHost, nvrUsername, nvrPassword)
	if err != nil {
		return err
	}
	defer client.close()
	files, err := client.recordings(channel, rangeStart, rangeEnd)
	if err != nil {
		return fmt.Errorf("find recording at requested time: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no recording exists at the requested time")
	}
	stream, err := client.openRecordingRange(files[0].Name, rangeStart, rangeEnd)
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
	setts := fmt.Sprintf("setts=pts=(N/%d+%d)/TB:dts=(N/%d+%d)/TB:duration=1/(%d*TB)", fps, offset, fps, offset, fps)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-hide_banner", "-loglevel", "warning", "-f", "h264", "-framerate", fpsText, "-i", "pipe:0", "-map", "0:v:0", "-an", "-t", strconv.Itoa(duration), "-c:v", "copy", "-bsf:v", setts, "-muxdelay", "0", "-f", "mpegts", output)
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
	if waitErr != nil {
		return fmt.Errorf("FFmpeg HLS segment: %s", stderr.String())
	}
	if writeErr != nil && writeErr != io.EOF {
		if _, statErr := os.Stat(output); statErr != nil {
			return writeErr
		}
	}
	info, err := os.Stat(output)
	if err != nil || info.Size() == 0 {
		return fmt.Errorf("recorder returned an empty HLS segment")
	}
	return nil
}
