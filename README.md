# CCTV Dashboard 0.1.0

Initial application build for the SANNCE NBD8904T-GS-XPOE recorder at `10.10.1.2`.

## Included

- Stable four-camera live view through go2rtc
- MSE/WebRTC/MJPEG fallback
- Application login with HttpOnly session cookies
- Digital zoom, snapshots, fullscreen and camera ordering
- Responsive desktop and mobile interface
- System dashboard
- Live TCP health checks for ports 554, 8181, 23000 and 34567
- Recorder model and firmware overview
- In-memory application audit events
- Camera, playback, recordings and settings application pages

Playback and recording search are placeholders in this milestone. The next phase is XMeye protocol discovery and recording catalogue integration over TCP/34567.

## Setup

```powershell
Copy-Item .env.example .env
```

Set a strong application password and session secret in `.env`. Existing camera RTSP URLs from the working enhanced viewer can be reused.

```powershell
docker compose config
docker compose up -d --build
```

Open:

```text
http://10.10.1.6:8080
```

## Upgrade from the enhanced viewer

Keep your existing `.env`, replace the remaining project files, then run:

```powershell
docker compose down
docker compose build --no-cache
docker compose up -d --force-recreate
```

## Useful checks

```powershell
docker compose ps
docker compose logs -f
docker compose exec web nginx -t
```
