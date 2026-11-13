# Pulsedeck wire protocol (v1)

How Pulsedeck servers and clients talk to each other. Anything that speaks
this protocol can be a client or a server.

## Discovery

The server announces the DNS-SD service `_pulsedeck._tcp` on the local network.
The instance name is the server's display name, the port is the HTTP port
(default `7878`), and the TXT record carries `proto=1` and `v=<server version>`.

## Transport

One WebSocket at `ws://<host>:<port>/ws`. All frames are UTF-8 JSON objects
with a `type` field. Unknown types and unknown fields must be ignored, which is
how the protocol grows without breaking old clients.

The server rejects WebSocket upgrades whose `Origin` is a public website, so a
page on the internet cannot read a visitor's machine stats. Requests with no
`Origin` (native clients), the server's own pages, `http://pulsedeck.app` (the
mobile shells) and origins on loopback / private addresses are accepted.

## Server → client

### `hello` (once, immediately after connecting)

```json
{
  "type": "hello",
  "proto": 1,
  "server": { "id": "k3v9x2mh7qpa", "name": "studio-mac", "os": "darwin", "version": "0.1.0" },
  "interval_ms": 1000,
  "sensors": [
    { "id": "cpu.total", "label": "CPU", "group": "CPU", "unit": "%", "kind": "value", "min": 0, "max": 100 }
  ],
  "actions": [ { "id": "playpause", "label": "Play / Pause", "icon": "play" } ],
  "history": { "t": [1794588379774], "v": { "cpu.total": [31.9] } }
}
```

* `server.id` identifies the installation. One address can answer as different
  installations over time (a dual-boot PC); clients key saved dashboards on it.
* `sensors[].kind` is `value` (number, or short text for plugins) or `list`.
* `max: 0` means the reading has no natural upper bound; clients auto-scale.
* `unit` is one of `%`, `B`, `B/s`, `°C`, `W`, `MHz`, `rpm`, `s`, or empty / free text.
  Clients are responsible for humanising (`B` → `GB`, `s` → `1d 9h`).
* `history` holds up to 120 recent samples so graphs start populated.
  Series are aligned with `t`; a missing sample is `null`.
* Actions never expose what they run, only an id, a label and an icon hint.

### `data` (every `interval_ms`)

```json
{ "type": "data", "t": 1794588380774, "v": { "cpu.total": 20.3, "proc.top": [ { "name": "Terminal", "cpu": 24.1, "mem": 40999936 } ] } }
```

`v` maps sensor ids to their current reading. A sensor may be absent from a
frame (slow sources refresh every few seconds); clients keep the last value.

### `action_result`

```json
{ "type": "action_result", "id": "playpause", "n": 7, "ok": false, "error": "not paired: …" }
```

## Client → server

### `action`

```json
{ "type": "action", "id": "playpause", "n": 7, "token": "<pairing token>" }
```

`n` is a client-chosen correlation number echoed in the result. `token` must
match the server's pairing token. Monitoring needs no token; anything that
*does* something on the computer does.

## Built-in sensor ids

| id | meaning |
| --- | --- |
| `cpu.total`, `cpu.core.<n>`, `cpu.load1` | CPU usage and load |
| `cpu.freq`, `cpu.freq.<n>` | average and per-core clock speed (MHz) |
| `cpu.power`, `sys.power` | CPU package power and whole-system power (W) |
| `mem.used`, `mem.used_pct`, `mem.swap_pct` | memory actually in use (bytes; excludes reclaimable cache) |
| `disk.<mount>.used_pct`, `disk.<mount>.free`, `disk.read`, `disk.write` | storage |
| `net.rx`, `net.tx` | network throughput (physical interfaces) |
| `temp.cpu`, `temp.cpu_max`, `temp.gpu`, `temp.ssd`, `temp.battery`, `temp.max` | temperature summaries: average per component, hottest core, hottest sensor |
| `temp.<sensor>` | individual sensors, where the OS names them usefully |
| `fan.<n>` | fan speed (rpm) |
| `gpu.<n>.util` / `mem` / `mem_pct` / `temp` / `power` / `fan` | GPU usage, memory in use, temperature, package power |
| `sys.uptime`, `sys.procs`, `proc.top` | system |
| `plugin.<id>` | user-defined plugin output |
