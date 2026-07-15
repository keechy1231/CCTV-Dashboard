package main

import (
	"bytes"
	"errors"
	"io"
)

// h264SyncWriter discards leading P-frames from DVRIP sessions that begin in
// the middle of a GOP. Browsers and MP4 muxers need SPS/PPS metadata first.
type h264SyncWriter struct {
	dst    io.Writer
	buffer []byte
	synced bool
}

func newH264SyncWriter(dst io.Writer) *h264SyncWriter { return &h264SyncWriter{dst: dst} }

func (w *h264SyncWriter) Write(p []byte) (int, error) {
	if w.synced {
		_, err := w.dst.Write(p)
		return len(p), err
	}
	w.buffer = append(w.buffer, p...)
	index := bytes.Index(w.buffer, []byte{0, 0, 0, 1, 0x67})
	if index < 0 {
		index = bytes.Index(w.buffer, []byte{0, 0, 1, 0x67})
	}
	if index >= 0 {
		w.synced = true
		_, err := w.dst.Write(w.buffer[index:])
		w.buffer = nil
		return len(p), err
	}
	if len(w.buffer) > 32*1024*1024 {
		return len(p), errors.New("no H.264 keyframe metadata found in the recording")
	}
	return len(p), nil
}
