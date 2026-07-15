package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

const playbackCacheDir = "/var/cache/cctv"

type cacheJob struct {
	Key      string `json:"key"`
	Status   string `json:"status"`
	Bytes    int64  `json:"bytes"`
	Total    int64  `json:"total"`
	Progress int    `json:"progress"`
	FPS      int    `json:"fps,omitempty"`
	Error    string `json:"error,omitempty"`
	Name     string `json:"-"`
}

type cacheManager struct {
	mu     sync.Mutex
	jobs   map[string]*cacheJob
	cancel context.CancelFunc
}

var playbackCache = cacheManager{jobs: make(map[string]*cacheJob)}

type progressWriter struct {
	dst io.Writer
	job *cacheJob
	mgr *cacheManager
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	w.mgr.mu.Lock()
	w.job.Bytes += int64(n)
	if w.job.Total > 0 {
		progress := int(w.job.Bytes * 100 / w.job.Total)
		if progress > 99 {
			progress = 99
		}
		w.job.Progress = progress
	}
	w.mgr.mu.Unlock()
	return n, err
}

func cacheKey(name string) string {
	sum := sha256.Sum256([]byte("archive-keepalive-v3:" + name))
	return hex.EncodeToString(sum[:])
}

func cachedPath(key string) string { return filepath.Join(playbackCacheDir, key+".mp4") }

func (m *cacheManager) prepare(name string, totalKB int64) cacheJob {
	key := cacheKey(name)
	m.mu.Lock()
	if info, err := os.Stat(cachedPath(key)); err == nil && info.Size() > 0 {
		job := &cacheJob{Key: key, Name: name, Status: "ready", Bytes: info.Size(), Total: info.Size(), Progress: 100}
		m.jobs[key] = job
		result := *job
		m.mu.Unlock()
		return result
	}
	if existing := m.jobs[key]; existing != nil && existing.Status == "preparing" {
		result := *existing
		m.mu.Unlock()
		return result
	}
	if m.cancel != nil {
		m.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	job := &cacheJob{Key: key, Name: name, Status: "preparing", Total: totalKB * 1024}
	m.jobs[key] = job
	result := *job
	m.mu.Unlock()
	go m.run(ctx, job)
	return result
}

func (m *cacheManager) status(key string) (cacheJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[key]
	if job == nil {
		if info, err := os.Stat(cachedPath(key)); err == nil && info.Size() > 0 {
			return cacheJob{Key: key, Status: "ready", Bytes: info.Size(), Total: info.Size(), Progress: 100}, true
		}
		return cacheJob{}, false
	}
	return *job, true
}

func (m *cacheManager) run(ctx context.Context, job *cacheJob) {
	part := cachedPath(job.Key) + ".part"
	_ = os.Remove(part)
	err := cacheRecording(ctx, job, part, m)
	if err == nil {
		err = os.Rename(part, cachedPath(job.Key))
	}
	if err != nil {
		_ = os.Remove(part)
	}
	m.mu.Lock()
	if err != nil {
		job.Status = "error"
		if ctx.Err() != nil {
			job.Error = "Preparation cancelled"
		} else {
			job.Error = err.Error()
		}
		log.Printf("playback cache failed for %q: %v", job.Name, err)
	} else {
		job.Status, job.Progress = "ready", 100
		if info, statErr := os.Stat(cachedPath(job.Key)); statErr == nil {
			job.Bytes = info.Size()
			job.Total = info.Size()
		}
	}
	m.mu.Unlock()
}

func cacheRecording(ctx context.Context, job *cacheJob, output string, manager *cacheManager) error {
	client, err := dialDVRIP(nvrHost, nvrUsername, nvrPassword)
	if err != nil {
		return err
	}
	defer client.close()
	stream, err := client.openRecording(job.Name)
	if err != nil {
		return err
	}
	defer stream.Close()
	go func() { <-ctx.Done(); _ = stream.Close() }()
	fps, prefix, ended, err := stream.ProbeVideo()
	if err != nil {
		return err
	}
	manager.mu.Lock()
	job.FPS = fps
	manager.mu.Unlock()
	fpsText := fmt.Sprint(fps)
	setts := fmt.Sprintf("setts=pts=N/(%d*TB):dts=N/(%d*TB):duration=1/(%d*TB)", fps, fps, fps)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-hide_banner", "-loglevel", "warning", "-f", "h264", "-framerate", fpsText, "-i", "pipe:0", "-map", "0:v:0", "-an", "-c:v", "copy", "-bsf:v", setts, "-r", fpsText, "-movflags", "+faststart", "-f", "mp4", output)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	writeErr := stream.WriteRemainingTo(newH264SyncWriter(&progressWriter{dst: stdin, job: job, mgr: manager}), prefix, ended)
	_ = stdin.Close()
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if writeErr != nil && writeErr != io.EOF {
		return writeErr
	}
	if waitErr != nil {
		return fmt.Errorf("FFmpeg: %s", stderr.String())
	}
	return nil
}

type limitedBuffer struct{ data []byte }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	if len(b.data) > 4096 {
		b.data = b.data[len(b.data)-4096:]
	}
	return len(p), nil
}
func (b *limitedBuffer) String() string { return string(b.data) }
