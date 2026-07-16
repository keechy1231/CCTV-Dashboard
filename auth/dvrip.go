package main

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const dvripPort = 34567

type dvripClient struct {
	conn    net.Conn
	session uint32
	seq     uint32
	alive   time.Duration
}

type dvripStream struct {
	conn net.Conn
	stop chan struct{}
	once sync.Once
}

type recording struct {
	Name      string `json:"name"`
	Disk      int    `json:"disk"`
	Partition int    `json:"partition"`
	SizeKB    int64  `json:"size_kb"`
	Start     string `json:"start"`
	End       string `json:"end"`
	Channel   int    `json:"channel"`
}

type dvripReply struct {
	Ret           int    `json:"Ret"`
	SessionID     string `json:"SessionID"`
	AliveInterval int    `json:"AliveInterval"`
	ChannelNum    int    `json:"ChannelNum"`
	Files         []struct {
		FileName   string      `json:"FileName"`
		DiskNo     int         `json:"DiskNo"`
		SerialNo   int         `json:"SerialNo"`
		FileLength interface{} `json:"FileLength"`
		BeginTime  string      `json:"BeginTime"`
		EndTime    string      `json:"EndTime"`
	} `json:"OPFileQuery"`
}

func xmMD5(password string) string {
	sum := md5.Sum([]byte(password)) // DVRIP requires this legacy, device-specific password transform.
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var b strings.Builder
	for i := 0; i < len(sum); i += 2 {
		b.WriteByte(alphabet[(int(sum[i])+int(sum[i+1]))%len(alphabet)])
	}
	return b.String()
}

func dialDVRIP(host, user, pass string) (*dvripClient, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(dvripPort)), 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to recorder: %w", err)
	}
	client := &dvripClient{conn: conn}
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	reply, err := client.request(1000, map[string]any{
		"UserName": user, "PassWord": xmMD5(pass), "EncryptType": "MD5", "LoginType": "DVRIP-Web",
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("recorder login: %w", err)
	}
	if reply.Ret != 100 {
		conn.Close()
		return nil, fmt.Errorf("recorder rejected login (code %d)", reply.Ret)
	}
	var session uint32
	if _, err := fmt.Sscanf(reply.SessionID, "0x%08X", &session); err != nil {
		conn.Close()
		return nil, errors.New("recorder returned an invalid session")
	}
	client.session = session
	client.alive = time.Duration(reply.AliveInterval) * time.Second
	if client.alive < 5*time.Second {
		client.alive = 20 * time.Second
	}
	_ = conn.SetDeadline(time.Time{})
	return client, nil
}

func (c *dvripClient) close() { _ = c.conn.Close() }

func (c *dvripClient) request(command uint16, body any) (*dvripReply, error) {
	c.seq += 2
	if err := writeDVRIPPacket(c.conn, c.session, c.seq, command, body); err != nil {
		return nil, err
	}
	return readDVRIPReply(c.conn, command+1)
}

func writeDVRIPPacket(conn net.Conn, session, seq uint32, command uint16, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	payload = append(payload, '\n', 0)
	header := make([]byte, 20)
	header[0], header[1] = 0xff, 0x01
	binary.LittleEndian.PutUint32(header[4:8], session)
	binary.LittleEndian.PutUint32(header[8:12], seq)
	binary.LittleEndian.PutUint16(header[14:16], command)
	binary.LittleEndian.PutUint32(header[16:20], uint32(len(payload)))
	_, err = conn.Write(append(header, payload...))
	return err
}

func readDVRIPReply(conn net.Conn, expected uint16) (*dvripReply, error) {
	header := make([]byte, 20)
	for {
		if _, err := io.ReadFull(conn, header); err != nil {
			return nil, err
		}
		if header[0] != 0xff {
			return nil, errors.New("invalid DVRIP response header")
		}
		length := binary.LittleEndian.Uint32(header[16:20])
		if length > 8*1024*1024 {
			return nil, errors.New("DVRIP response is too large")
		}
		data := make([]byte, length)
		if _, err := io.ReadFull(conn, data); err != nil {
			return nil, err
		}
		responseCommand := binary.LittleEndian.Uint16(header[14:16])
		if responseCommand != expected {
			continue
		}
		data = []byte(strings.TrimRight(string(data), "\x00\r\n"))
		var reply dvripReply
		if err := json.Unmarshal(data, &reply); err != nil {
			return nil, fmt.Errorf("decode DVRIP response: %w", err)
		}
		return &reply, nil
	}
}

func (c *dvripClient) openRecording(name string) (*dvripStream, error) {
	return c.openRecordingRange(name, time.Time{}, time.Time{})
}

func (c *dvripClient) openRecordingRange(name string, rangeStart, rangeEnd time.Time) (*dvripStream, error) {
	dataConn, err := net.DialTimeout("tcp", net.JoinHostPort(nvrHost, fmt.Sprint(dvripPort)), 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("open recorder media connection: %w", err)
	}
	_ = dataConn.SetDeadline(time.Now().Add(12 * time.Second))
	startTime, endTime := playbackTimeRange(name)
	if !rangeStart.IsZero() && rangeEnd.After(rangeStart) {
		startTime = rangeStart.Format("2006-01-02 15:04:05")
		endTime = rangeEnd.Format("2006-01-02 15:04:05")
	}
	payload := map[string]any{
		"Name": "OPPlayBack", "SessionID": fmt.Sprintf("0x%08X", c.session),
		"OPPlayBack": map[string]any{
			"Action": "DownloadStart", "Parameter": map[string]any{
				"FileName": name, "TransMode": "TCP", "PlayMode": "ByName", "Value": 0,
			},
			"StartTime": startTime, "EndTime": endTime,
		},
	}
	if err := writeDVRIPPacket(dataConn, c.session, 0, 1424, payload); err != nil {
		dataConn.Close()
		return nil, err
	}
	reply, err := c.request(1420, payload)
	if err != nil || reply.Ret != 100 {
		dataConn.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("recorder refused playback (code %d)", reply.Ret)
	}
	claim, err := readDVRIPReply(dataConn, 1425)
	if err != nil || claim.Ret != 100 {
		dataConn.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("recorder refused media connection (code %d)", claim.Ret)
	}
	_ = dataConn.SetDeadline(time.Time{})
	stream := &dvripStream{conn: dataConn, stop: make(chan struct{})}
	go c.keepPlaybackAlive(stream.stop)
	return stream, nil
}

var playbackFileTime = regexp.MustCompile(`/(\d{4})-(\d{2})-(\d{2})/\d+/(\d{2})\.(\d{2})\.(\d{2})-(\d{2})\.(\d{2})\.(\d{2})`)

func playbackTimeRange(name string) (string, string) {
	start, end, ok := playbackTimes(name)
	if !ok {
		return "2000-00-00 00:00:00", "9999-12-31 23:59:59"
	}
	return start.Format("2006-01-02 15:04:05"), end.Format("2006-01-02 15:04:05")
}

func playbackTimes(name string) (time.Time, time.Time, bool) {
	m := playbackFileTime.FindStringSubmatch(strings.ReplaceAll(name, `\`, "/"))
	if m == nil {
		return time.Time{}, time.Time{}, false
	}
	numbers := make([]int, 9)
	for i := range numbers {
		numbers[i], _ = strconv.Atoi(m[i+1])
	}
	start := time.Date(numbers[0], time.Month(numbers[1]), numbers[2], numbers[3], numbers[4], numbers[5], 0, time.Local)
	end := time.Date(numbers[0], time.Month(numbers[1]), numbers[2], numbers[6], numbers[7], numbers[8], 0, time.Local)
	if !end.After(start) {
		end = end.AddDate(0, 0, 1)
	}
	return start, end, true
}

func (c *dvripClient) keepPlaybackAlive(stop <-chan struct{}) {
	interval := c.alive / 2
	if interval < 3*time.Second {
		interval = 3 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if _, err := c.request(1006, map[string]any{
				"Name": "KeepAlive", "SessionID": fmt.Sprintf("0x%08X", c.session),
			}); err != nil {
				return
			}
		}
	}
}

func (s *dvripStream) Close() error {
	s.once.Do(func() { close(s.stop) })
	return s.conn.Close()
}

func (s *dvripStream) readMediaPayload() ([]byte, bool, error) {
	header := make([]byte, 20)
	for {
		if _, err := io.ReadFull(s.conn, header); err != nil {
			return nil, false, err
		}
		if header[0] != 0xff {
			return nil, false, errors.New("invalid DVRIP media header")
		}
		length := binary.LittleEndian.Uint32(header[16:20])
		if length > 8*1024*1024 {
			return nil, false, errors.New("DVRIP media packet is too large")
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(s.conn, payload); err != nil {
			return nil, false, err
		}
		if binary.LittleEndian.Uint16(header[14:16]) != 1426 {
			continue
		}
		return payload, header[13] != 0, nil
	}
}

func (s *dvripStream) ProbeVideo() (int, []byte, bool, error) {
	var buffered []byte
	for len(buffered) < 32*1024*1024 {
		payload, end, err := s.readMediaPayload()
		if err != nil {
			return 0, nil, false, err
		}
		buffered = append(buffered, payload...)
		for i := 0; i+16 <= len(buffered); i++ {
			if buffered[i] == 0 && buffered[i+1] == 0 && buffered[i+2] == 1 && (buffered[i+3] == 0xfc || buffered[i+3] == 0xfe) {
				fps := int(buffered[i+5])
				if fps >= 1 && fps <= 60 {
					return fps, buffered, end, nil
				}
			}
		}
		if end {
			return 0, nil, true, errors.New("recording ended before video metadata")
		}
	}
	return 0, nil, false, errors.New("no DVRIP video metadata found")
}

func (s *dvripStream) WriteRemainingTo(w io.Writer, prefix []byte, ended bool) error {
	if len(prefix) > 0 {
		if _, err := w.Write(prefix); err != nil {
			return err
		}
	}
	if ended {
		return nil
	}
	for {
		payload, end, err := s.readMediaPayload()
		if err != nil {
			return err
		}
		if len(payload) > 0 {
			if _, err := w.Write(payload); err != nil {
				return err
			}
		}
		if end {
			return nil
		}
	}
}

func (s *dvripStream) WriteTo(w io.Writer) error {
	_, prefix, ended, err := s.ProbeVideo()
	if err != nil {
		return err
	}
	return s.WriteRemainingTo(w, prefix, ended)
}

func parseDVRIPSize(value interface{}) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case string:
		var n int64
		if strings.HasPrefix(v, "0x") {
			_, _ = fmt.Sscanf(v, "0x%x", &n)
		} else {
			_, _ = fmt.Sscan(v, &n)
		}
		return n
	default:
		return 0
	}
}

func (c *dvripClient) recordings(channel int, start, end time.Time) ([]recording, error) {
	session := fmt.Sprintf("0x%08X", c.session)
	reply, err := c.request(1440, map[string]any{
		"Name": "OPFileQuery", "SessionID": session,
		"OPFileQuery": map[string]any{
			"BeginTime": start.Format("2006-01-02 15:04:05"), "EndTime": end.Format("2006-01-02 15:04:05"),
			"Channel": channel, "Event": "*", "Type": "h264",
		},
	})
	if err != nil {
		return nil, err
	}
	if reply.Ret != 100 && reply.Ret != 110 {
		return nil, fmt.Errorf("recording search failed (code %d)", reply.Ret)
	}
	result := make([]recording, 0, len(reply.Files))
	for _, file := range reply.Files {
		result = append(result, recording{Name: file.FileName, Disk: file.DiskNo, Partition: file.SerialNo, SizeKB: parseDVRIPSize(file.FileLength), Start: file.BeginTime, End: file.EndTime, Channel: channel})
	}
	return result, nil
}
