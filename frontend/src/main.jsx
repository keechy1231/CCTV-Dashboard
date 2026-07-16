import React, {
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import { createRoot } from "react-dom/client";
import Hls from "hls.js";
import {
  Activity,
  CalendarClock,
  Camera,
  ChevronRight,
  Download,
  Gauge,
  Grid2X2,
  LayoutDashboard,
  LogOut,
  Maximize2,
  Minimize2,
  Menu,
  MonitorPlay,
  RefreshCw,
  RotateCcw,
  Server,
  Settings,
  ShieldCheck,
  X,
  ZoomIn,
  ZoomOut,
} from "lucide-react";
import "./styles.css";
import "./playback.css";
import "./live.css";

const PREF_KEY = "cctv-dashboard-prefs-v3";
const defaults = {
  columns: 2,
  showClock: true,
  order: [],
};
async function api(path, options = {}) {
  const r = await fetch(path, {
    ...options,
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
  });
  const t = r.headers.get("content-type") || "";
  const b = t.includes("application/json") ? await r.json() : null;
  if (!r.ok) throw new Error(b?.error || `Request failed (${r.status})`);
  return b;
}
function stored() {
  try {
    const value = {
      ...defaults,
      ...JSON.parse(localStorage.getItem(PREF_KEY) || "{}"),
    };
    return value;
  } catch {
    return defaults;
  }
}

function Login({ done }) {
  const [u, setU] = useState("admin"),
    [p, setP] = useState(""),
    [remember, setRemember] = useState(true),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  async function submit(e) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api("/api/login", {
        method: "POST",
        body: JSON.stringify({ username: u, password: p, remember }),
      });
      done();
    } catch (x) {
      setError(x.message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <main className="login">
      <section className="login-card">
        <div className="logo">
          <Camera />
        </div>
        <h1>CCTV Dashboard</h1>
        <p>Secure access to your local surveillance system.</p>
        <form onSubmit={submit}>
          <label>
            Username
            <input
              value={u}
              onChange={(e) => setU(e.target.value)}
              autoComplete="username"
            />
          </label>
          <label>
            Password
            <input
              value={p}
              onChange={(e) => setP(e.target.value)}
              type="password"
              autoComplete="current-password"
              autoFocus
            />
          </label>
          <label className="check">
            <input
              type="checkbox"
              checked={remember}
              onChange={(e) => setRemember(e.target.checked)}
            />
            Keep me signed in
          </label>
          {error && <div className="error">{error}</div>}
          <button disabled={busy}>{busy ? "Signing in…" : "Sign in"}</button>
        </form>
        <small>
          <ShieldCheck size={14} /> Access restricted to authorised users
        </small>
      </section>
    </main>
  );
}

function NativePlayer({ camera, zoom, setZoom, onState, reload }) {
  const ref = useRef(null);
  useEffect(() => {
    let alive = true,
      el,
      timer;
    (async () => {
      const playerModule = "/rtc/video-rtc.js";
      const mod = await import(/* @vite-ignore */ playerModule);
      if (!customElements.get("cctv-video"))
        customElements.define("cctv-video", class extends mod.VideoRTC {});
      if (!alive || !ref.current) return;
      ref.current.replaceChildren();
      el = document.createElement("cctv-video");
      el.mode = "mse";
      el.media = "video";
      el.background = true;
      el.visibilityCheck = false;
      el.src = `/rtc/api/ws?src=${camera.id}`;
      ref.current.appendChild(el);
      onState("connecting");
      timer = setInterval(() => {
        const v = el.video || el.querySelector("video");
        if (!v) return;
        clearInterval(timer);
        v.muted = true;
        v.playsInline = true;
        v.addEventListener("playing", () => onState("live"));
        v.addEventListener("waiting", () => onState("buffering"));
        v.addEventListener("stalled", () => onState("buffering"));
        v.addEventListener("error", () => onState("offline"));
      }, 150);
    })().catch(() => onState("offline"));
    return () => {
      alive = false;
      clearInterval(timer);
      el?.ondisconnect?.();
      el?.remove();
    };
  }, [camera.id, reload, onState]);
  return (
    <div
      className="native"
      onWheel={(e) => {
        e.preventDefault();
        setZoom((z) =>
          Math.max(
            1,
            Math.min(4, +(z + (e.deltaY < 0 ? 0.2 : -0.2)).toFixed(1)),
          ),
        );
      }}
    >
      <div ref={ref} style={{ transform: `scale(${zoom})` }} />
    </div>
  );
}
function Clock() {
  const [n, setN] = useState(new Date());
  useEffect(() => {
    const i = setInterval(() => setN(new Date()), 1000);
    return () => clearInterval(i);
  }, []);
  return n.toLocaleString("en-GB", { hour12: false });
}
function CameraCard({ camera, prefs, onMove }) {
  const [state, setState] = useState("connecting"),
    [reload, setReload] = useState(0),
    [zoom, setZoom] = useState(1),
    ref = useRef(null);
  const stable = useCallback(setState, []);
  async function shot() {
    const r = await fetch(`/rtc/api/frame.jpeg?src=${camera.id}`);
    if (!r.ok) return;
    const u = URL.createObjectURL(await r.blob()),
      a = document.createElement("a");
    a.href = u;
    a.download = `${camera.name}.jpg`;
    a.click();
    URL.revokeObjectURL(u);
  }
  return (
    <article
      className={`camera-card ${state}`}
      ref={ref}
      draggable
      onDragStart={(e) => e.dataTransfer.setData("camera", camera.id)}
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => onMove(e.dataTransfer.getData("camera"), camera.id)}
    >
      <header>
        <div>
          <span className="dot" />
          <strong>{camera.name}</strong>
          <em>{state}</em>
        </div>
        <div className="tools">
          <button onClick={() => setZoom((z) => Math.max(1, z - 0.25))}>
            <ZoomOut />
          </button>
          <button onClick={() => setZoom(1)}>
            <RotateCcw />
          </button>
          <button onClick={() => setZoom((z) => Math.min(4, z + 0.25))}>
            <ZoomIn />
          </button>
          <button onClick={shot}>
            <Download />
          </button>
          <button onClick={() => setReload((x) => x + 1)}>
            <RefreshCw />
          </button>
          <button onClick={() => ref.current?.requestFullscreen()}>
            <Maximize2 />
          </button>
        </div>
      </header>
      <div className="video-box">
        <NativePlayer
          camera={camera}
          zoom={zoom}
          setZoom={setZoom}
          onState={stable}
          reload={reload}
        />
        {prefs.showClock && (
          <span className="clock">
            <Clock />
          </span>
        )}
        {zoom > 1 && <span className="zoom">{zoom.toFixed(1)}×</span>}
      </div>
    </article>
  );
}

function LiveView({ cameras, prefs, setPrefs, onMove }) {
  const matrixRef = useRef(null);
  const [fullscreen, setFullscreen] = useState(false);
  useEffect(() => {
    const changed = () => setFullscreen(document.fullscreenElement === matrixRef.current);
    document.addEventListener("fullscreenchange", changed);
    return () => document.removeEventListener("fullscreenchange", changed);
  }, []);
  async function toggleFullscreen() {
    if (document.fullscreenElement) await document.exitFullscreen();
    else await matrixRef.current?.requestFullscreen();
  }
  return (
    <div className="page live-page">
      <div className="page-title">
        <div>
          <p>Live surveillance</p>
          <h1>Live view</h1>
        </div>
        <div className="layout">
          <select
            value={prefs.columns}
            onChange={(e) => setPrefs((p) => ({ ...p, columns: +e.target.value }))}
          >
            <option value="1">1 column</option>
            <option value="2">2 columns</option>
            <option value="3">3 columns</option>
          </select>
          <button type="button" onClick={toggleFullscreen} title="Show all cameras fullscreen" aria-pressed={fullscreen}>
            <Maximize2 /> Fullscreen grid
          </button>
        </div>
      </div>
      <div className="live-matrix" ref={matrixRef}>
        <div className="matrix-fullscreen-bar">
          <strong>Live view · {cameras.length} cameras</strong>
          <button type="button" onClick={toggleFullscreen}>
            <Minimize2 /> Exit fullscreen
          </button>
        </div>
        <section className="camera-grid" style={{ "--cols": prefs.columns }}>
          {cameras.map((camera) => (
            <CameraCard key={camera.id} camera={camera} prefs={prefs} onMove={onMove} />
          ))}
        </section>
      </div>
    </div>
  );
}

const nav = [
  ["dashboard", "Overview", LayoutDashboard],
  ["live", "Live view", Grid2X2],
  ["playback", "Playback", MonitorPlay],
  ["events", "Events", Activity],
  ["cameras", "Cameras", Camera],
  ["settings", "Settings", Settings],
];
function ArchivePage({ cameras }) {
  return (
    <div className="page">
      <div className="page-title">
        <div>
          <p>NVR archive</p>
          <h1>Playback</h1>
        </div>
      </div>
      <DailyPlayback cameras={cameras} />
    </div>
  );
}

function DailyPlayback({ cameras }) {
  const today = new Date().toLocaleDateString("en-CA");
  const [channel, setChannel] = useState(0);
  const [date, setDate] = useState(today);
  const [error, setError] = useState("");
  const videoRef = useRef(null);
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    setError("");
    const source = `/api/playback/hls?${new URLSearchParams({ channel: String(channel), date })}`;
    if (Hls.isSupported()) {
      let recoveries = 0;
      const hls = new Hls({
        maxBufferLength: 30,
        maxMaxBufferLength: 30,
        backBufferLength: 30,
        startFragPrefetch: false,
        fragLoadingMaxRetry: 4,
        fragLoadingRetryDelay: 750,
        fragLoadingMaxRetryTimeout: 5000,
      });
      hls.loadSource(source);
      hls.attachMedia(video);
      hls.on(Hls.Events.ERROR, (_event, data) => {
        if (!data.fatal) return;
        if (data.type === Hls.ErrorTypes.NETWORK_ERROR && recoveries < 3) {
          recoveries++;
          setError("The recorder is busy; retrying this time…");
          setTimeout(() => hls.startLoad(video.currentTime), 750 * recoveries);
        } else if (data.type === Hls.ErrorTypes.MEDIA_ERROR && recoveries < 3) {
          recoveries++;
          hls.recoverMediaError();
        } else {
          setError(`Playback could not continue (${data.details}).`);
        }
      });
      hls.on(Hls.Events.FRAG_LOADED, () => {
        recoveries = 0;
        setError("");
      });
      return () => hls.destroy();
    }
    if (video.canPlayType("application/vnd.apple.mpegurl")) {
      video.src = source;
      return () => {
        video.pause();
        video.removeAttribute("src");
      };
    }
    setError("This browser does not support timeline playback.");
  }, [channel, date]);
  return (
    <div className="daily-playback">
      <section className="panel daily-playback-controls">
        <label>
          Camera
          <select value={channel} onChange={(e) => setChannel(Number(e.target.value))}>
            {cameras.map((camera, index) => <option key={camera.id} value={index}>{camera.name}</option>)}
          </select>
        </label>
        <label>
          Date
          <input type="date" value={date} max={today} onChange={(e) => setDate(e.target.value)} />
        </label>
        <span>Use the video timeline to jump directly to any recorded time.</span>
      </section>
      <section className="panel playback-stage daily-playback-video">
        <video ref={videoRef} controls autoPlay playsInline />
        {error && <div className="error recording-error">{error}</div>}
      </section>
    </div>
  );
}
function StatusPill({ online }) {
  return (
    <span className={`pill ${online ? "ok" : "bad"}`}>
      {online ? "Online" : "Offline"}
    </span>
  );
}

function Dashboard({ cameras, status, go }) {
  const online = status?.services?.filter((x) => x.online).length || 0;
  return (
    <div className="page">
      <div className="page-title">
        <div>
          <p>System overview</p>
          <h1>Good evening</h1>
        </div>
        <button onClick={() => go("live")}>
          Open live view <ChevronRight />
        </button>
      </div>
      <div className="metrics">
        <div>
          <Camera />
          <span>
            <b>{cameras.length}</b>Configured cameras
          </span>
        </div>
        <div>
          <Server />
          <span>
            <b>{online}/4</b>NVR services online
          </span>
        </div>
        <div>
          <ShieldCheck />
          <span>
            <b>Protected</b>Local authentication
          </span>
        </div>
        <div>
          <CalendarClock />
          <span>
            <b>Available</b>Recording playback
          </span>
        </div>
      </div>
      <div className="dashboard-grid">
        <section className="panel">
          <header>
            <h2>NVR services</h2>
            <span>{status?.nvr_host}</span>
          </header>
          {status?.services?.map((s) => (
            <div className="service" key={s.port}>
              <div>
                <b>{s.name}</b>
                <small>
                  TCP/{s.port} ·{" "}
                  {s.online ? `${s.latency_ms} ms` : "No response"}
                </small>
              </div>
              <StatusPill online={s.online} />
            </div>
          ))}
        </section>
        <section className="panel">
          <header>
            <h2>Recorder</h2>
          </header>
          <dl>
            <div>
              <dt>Model</dt>
              <dd>{status?.model || "NBD8904T-GS-XPOE"}</dd>
            </div>
            <div>
              <dt>Firmware</dt>
              <dd>{status?.firmware || "Loading…"}</dd>
            </div>
            <div>
              <dt>Platform</dt>
              <dd>XMeye family</dd>
            </div>
            <div>
              <dt>Recording API</dt>
              <dd>Connected</dd>
            </div>
          </dl>
        </section>
        <section className="panel span">
          <header>
            <h2>Available features</h2>
          </header>
          <div className="roadmap">
            <div className="done">
              <ShieldCheck />
              <b>Live view</b>
              <span>
                Stable go2rtc playback, snapshots, zoom and fullscreen
              </span>
            </div>
            <div className="done">
              <Gauge />
              <b>System health</b>
              <span>Direct service checks against the NVR</span>
            </div>
            <div className="done">
              <CalendarClock />
              <b>Playback</b>
              <span>Search and play recordings stored on the NVR</span>
            </div>
          </div>
        </section>
      </div>
    </div>
  );
}

function App() {
  const [auth, setAuth] = useState("loading"),
    [page, setPage] = useState("dashboard"),
    [cameras, setCameras] = useState([]),
    [prefs, setPrefs] = useState(stored),
    [status, setStatus] = useState(null),
    [events, setEvents] = useState([]),
    [menu, setMenu] = useState(false);
  useEffect(
    () => localStorage.setItem(PREF_KEY, JSON.stringify(prefs)),
    [prefs],
  );
  async function load() {
    try {
      await api("/api/session");
      const c = await fetch("/config.json", { cache: "no-store" }).then((r) =>
        r.json(),
      );
      const positions = new Map(prefs.order.map((id, index) => [id, index]));
      setCameras(
        [...c.cameras].sort(
          (a, b) =>
            (positions.get(a.id) ?? Number.MAX_SAFE_INTEGER) -
            (positions.get(b.id) ?? Number.MAX_SAFE_INTEGER),
        ),
      );
      setAuth("ready");
    } catch {
      setAuth("login");
    }
  }
  useEffect(() => {
    load();
  }, []);
  useEffect(() => {
    if (auth !== "ready") return;
    const get = () =>
      api("/api/system/status")
        .then(setStatus)
        .catch(() => {});
    get();
    const i = setInterval(get, 30000);
    return () => clearInterval(i);
  }, [auth]);
  useEffect(() => {
    if (page === "events" && auth === "ready")
      api("/api/events")
        .then((x) => setEvents(x.events))
        .catch(() => {});
  }, [page, auth]);
  function move(a, b) {
    setCameras((x) => {
      const n = [...x],
        i = n.findIndex((c) => c.id === a),
        j = n.findIndex((c) => c.id === b);
      if (i < 0 || j < 0) return x;
      const [t] = n.splice(i, 1);
      n.splice(j, 0, t);
      setPrefs((p) => ({ ...p, order: n.map((c) => c.id) }));
      return n;
    });
  }
  async function logout() {
    await api("/api/logout", { method: "POST" }).catch(() => {});
    setAuth("login");
  }
  if (auth === "loading")
    return <main className="loading">Loading dashboard…</main>;
  if (auth === "login") return <Login done={load} />;
  let body;
  if (page === "dashboard")
    body = <Dashboard cameras={cameras} status={status} go={setPage} />;
  else if (page === "live")
    body = <LiveView cameras={cameras} prefs={prefs} setPrefs={setPrefs} onMove={move} />;
  else if (page === "events")
    body = (
      <div className="page">
        <div className="page-title">
          <div>
            <p>Application activity</p>
            <h1>Events</h1>
          </div>
        </div>
        <section className="panel event-list">
          {events.length ? (
            events.map((e, i) => (
              <div className="event" key={i}>
                <Activity />
                <div>
                  <b>{e.message}</b>
                  <small>
                    {new Date(e.time).toLocaleString("en-GB")} · {e.remote}
                  </small>
                </div>
                <span>{e.type}</span>
              </div>
            ))
          ) : (
            <p>No events recorded.</p>
          )}
        </section>
      </div>
    );
  else if (page === "cameras")
    body = (
      <div className="page">
        <div className="page-title">
          <div>
            <p>Configured sources</p>
            <h1>Cameras</h1>
          </div>
        </div>
        <div className="camera-admin">
          {cameras.map((c, i) => (
            <section className="panel" key={c.id}>
              <Camera />
              <div>
                <h3>{c.name}</h3>
                <p>
                  Channel {i + 1} · go2rtc source <code>{c.id}</code>
                </p>
              </div>
              <StatusPill online={true} />
            </section>
          ))}
        </div>
      </div>
    );
  else if (page === "playback")
    body = <ArchivePage cameras={cameras} />;
  else
    body = (
      <div className="page">
        <div className="page-title">
          <div>
            <p>Application preferences</p>
            <h1>Settings</h1>
          </div>
        </div>
        <section className="panel settings">
          <label>
            <input
              type="checkbox"
              checked={prefs.showClock}
              onChange={(e) =>
                setPrefs((p) => ({ ...p, showClock: e.target.checked }))
              }
            />{" "}
            Show timestamp overlay
          </label>
          <p className="note">Drag camera tiles in live view to change their order.</p>
        </section>
      </div>
    );
  return (
    <div className="shell">
      <aside className={menu ? "open" : ""}>
        <div className="brand">
          <Camera />
          <div>
            <b>CCTV</b>
            <span>Dashboard</span>
          </div>
          <button onClick={() => setMenu(false)}>
            <X />
          </button>
        </div>
        <nav>
          {nav.map(([id, label, I]) => (
            <button
              className={page === id ? "active" : ""}
              key={id}
              onClick={() => {
                setPage(id);
                setMenu(false);
              }}
            >
              <I />
              {label}
            </button>
          ))}
        </nav>
        <footer>
          <span>
            <i />
            System online
          </span>
          <button onClick={logout}>
            <LogOut />
            Log out
          </button>
        </footer>
      </aside>
      <main className="content">
        <header className="top">
          <button className="mobile" onClick={() => setMenu(true)}>
            <Menu />
          </button>
          <div>
            <b>{nav.find((x) => x[0] === page)?.[1]}</b>
            <span>Local NVR · {status?.nvr_host || "Connecting…"}</span>
          </div>
          <div className="top-status">
            <i />
            Connected
          </div>
        </header>
        {body}
      </main>
    </div>
  );
}
createRoot(document.getElementById("root")).render(<App />);
