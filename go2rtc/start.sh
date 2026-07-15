#!/bin/sh
set -eu
escape_yaml() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }
cat > /tmp/go2rtc.yaml <<CONFIG
api:
  listen: ":1984"
  origin: "*"
rtsp:
  listen: ":8554"
webrtc:
  listen: ":8555"
  candidates:
    - "${WEBRTC_HOST}:8555"
streams:
  camera1: "$(escape_yaml "$CAMERA_1_URL")#transport=tcp"
  camera2: "$(escape_yaml "$CAMERA_2_URL")#transport=tcp"
  camera3: "$(escape_yaml "$CAMERA_3_URL")#transport=tcp"
  camera4: "$(escape_yaml "$CAMERA_4_URL")#transport=tcp"
CONFIG
exec go2rtc -config /tmp/go2rtc.yaml
