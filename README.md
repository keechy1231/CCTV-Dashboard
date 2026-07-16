# CCTV Dashboard

A self-hosted web interface for viewing live CCTV cameras and searching and playing recordings stored on a compatible NVR. It is intended as a modern, browser-based replacement for older recorder interfaces and runs as a small Docker Compose application.

The project was developed and tested with a SANNCE NBD8904T-GS-XPOE recorder using the Xiongmai/XMeye DVRIP protocol. Other XMeye/DVRIP-compatible recorders may work, but models and firmware differ, so compatibility is not guaranteed.

## Features

- Four-camera live view through go2rtc
- MSE live playback through go2rtc
- Recording search by camera, date and time
- Browser playback of the NVR's archived H.264 recordings
- Local MP4 preparation and caching for reliable seeking
- Password-protected application login
- Digital zoom, snapshots, fullscreen view and camera ordering
- Responsive desktop and mobile interface
- Recorder status, model and firmware information
- In-memory application audit events

The application does not record camera video itself. Live video comes from the configured RTSP streams, and archived video is downloaded from the NVR when selected.

## How it works

Docker Compose starts three services:

- `cctv-web`: the React frontend and Nginx reverse proxy
- `cctv-auth`: login, NVR discovery, DVRIP recording search and FFmpeg playback preparation
- `cctv-go2rtc`: converts the configured RTSP streams into browser-compatible live video

Prepared recordings are stored in the Docker volume `playback-cache`. The first playback of a recording must be downloaded and prepared; reopening the same recording uses the cached copy.

## Requirements

- Docker Engine or Docker Desktop with Docker Compose
- An NVR reachable from the Docker host
- RTSP URLs for each camera or NVR channel
- For recording search and playback, an XMeye/DVRIP-compatible NVR reachable on TCP port `34567`
- A modern browser such as Firefox, Chrome or Edge
- Sufficient disk space for the playback cache; an hour of footage may use several hundred megabytes

The Docker host must be able to reach the NVR. The browser must also be able to reach the Docker host on the web port and the go2rtc WebRTC port (`8555` TCP/UDP).

## Installation

Clone the repository and enter its directory, then create your private environment file.

PowerShell:

```powershell
Copy-Item .env.example .env
```

Linux or macOS:

```sh
cp .env.example .env
```

Edit `.env` with the settings for your installation. Do not put real passwords in `.env.example`.

### Required application settings

```dotenv
APP_USERNAME=admin
APP_PASSWORD=choose-a-strong-login-password
SESSION_SECRET=replace-with-a-long-random-value
```

`APP_USERNAME` and `APP_PASSWORD` are used to sign in to the dashboard. `SESSION_SECRET` protects authenticated sessions and should be a unique random value of at least 32 characters. For example, a secret can be generated with:

```sh
openssl rand -hex 32
```

### Network settings

```dotenv
WEB_PORT=8080
WEBRTC_HOST=192.168.1.20
```

- `WEB_PORT` is the port used to open the dashboard.
- `WEBRTC_HOST` must be the IP address or hostname of the Docker host as seen by browser devices. It is not normally the NVR address.

For example, if Docker runs on `192.168.1.20` with `WEB_PORT=8080`, open `http://192.168.1.20:8080`.

### Camera settings

Set a display name and RTSP URL for each channel:

```dotenv
CAMERA_1_NAME=Front Camera
CAMERA_1_URL=rtsp://nvr-user:nvr-password@192.168.1.10:554/channel=1_stream=0.sdp?
```

Repeat this for `CAMERA_2` through `CAMERA_4`. RTSP paths vary between manufacturers; use the URLs supplied by the recorder or camera vendor. If a password contains URL-special characters, it may need to be URL-encoded.

### Recording playback settings

```dotenv
NVR_HOST=192.168.1.10
NVR_USERNAME=admin
NVR_PASSWORD=your-recorder-password
```

These credentials are used by the backend to query and download recordings over DVRIP. `NVR_HOST` should contain only the recorder hostname or IP address, without `http://` or a port.

## Start the application

Validate the configuration and build the containers:

```sh
docker compose config
docker compose up -d --build
```

Then open:

```text
http://DOCKER_HOST:WEB_PORT
```

Sign in using `APP_USERNAME` and `APP_PASSWORD` from `.env`.



Common issues:

- **Live video unavailable:** verify the RTSP URLs and confirm the Docker host can reach the NVR on port `554`.
- **WebRTC unavailable:** verify `WEBRTC_HOST` is the Docker host's reachable LAN address and allow TCP/UDP port `8555` through its firewall.
- **No recordings found:** verify the NVR credentials, recorder time, selected channel and access to TCP port `34567`.
- **Playback preparation is slow:** recordings are downloaded from the NVR and remuxed on first use. Speed depends on the recorder, network and recording size.
- **An old or incomplete recording reappears:** rebuild the application and retry. Prepared files are held in the `playback-cache` Docker volume.


## Compatibility notes

Recording playback depends on undocumented and firmware-dependent DVRIP behaviour. This build handles recorder session keepalives and remuxes downloaded H.264 video into MP4 without re-encoding. A recorder that uses a different protocol, media container or authentication flow may require additional support.

When reporting a compatibility issue, include the recorder model, firmware version, relevant container logs and a description of whether live view, recording search or playback failed. Remove passwords, authentication data and private camera URLs before sharing logs.
