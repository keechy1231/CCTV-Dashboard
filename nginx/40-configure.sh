#!/bin/sh
set -eu
cat > /usr/share/nginx/html/config.json <<JSON
{"cameras":[{"id":"camera1","name":"${CAMERA_1_NAME}"},{"id":"camera2","name":"${CAMERA_2_NAME}"},{"id":"camera3","name":"${CAMERA_3_NAME}"},{"id":"camera4","name":"${CAMERA_4_NAME}"}]}
JSON
