# splitdial

Userspace SOCKS5/HTTP proxy that pins each outbound connection to a chosen network interface, by rule.

![Go](https://img.shields.io/badge/go-1.25.3-00ADD8?logo=go&logoColor=white)
![License](https://img.shields.io/badge/license-MIT-blue)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)

## The problem

A laptop with two uplinks — say a corporate Ethernet dongle and a personal Wi-Fi — has exactly one
default route. Everything leaves through whichever interface the OS ranked first, regardless of where
it is going. You want per-destination control: internal hostnames and the VPN-less corporate services
out the Ethernet link, everything bulk or personal out Wi-Fi. The usual fixes all cost something —
policy routing needs root and survives reboots badly, a VPN client owns the whole default route, and
neither is easy to change per-destination on the fly. splitdial does it in userspace: a local proxy
that decides, per connection, which local interface address to bind before connecting.

## Why not just use X

| Approach | Better than splitdial at | Worse at |
|---|---|---|
| OS policy routing (`ip rule` / `pf` + route tables) | Transparent — catches *all* traffic, including UDP and non-proxy-aware apps. No proxy hop. | Needs root, persists poorly across reboots/DHCP, no per-domain rules (kernel sees IPs only), fiddly to change live. |
| VPN split tunnelling | Encrypts, gives you a remote exit IP, works app-transparently. | Takes over the default route, needs a VPN endpoint, usually needs a privileged tunnel device. |
| Clash / Surge / sing-box | Far richer rule engines, GeoIP, TUN mode, remote proxy chains, mature ecosystems. | Built around *remote* proxies; local-interface egress is a secondary concern or absent. Much larger surface. |

splitdial's niche is narrow and worth stating plainly: **userspace, no root, no kernel routing
changes, per-connection local-interface binding, driven by a YAML file that hot-reloads.** It does
not encrypt, does not tunnel, and only handles TCP through applications that speak SOCKS5 or HTTP proxy.

## How it works

```
  client                splitdial                                    kernel
 ───────────────────────────────────────────────────────────────────────────────
  SOCKS5 CONNECT  ──►  listener ──►  router.Route(host, port)
  or HTTP CONNECT      :1080/:8080   first enabled matching rule
                                     └─► "cable" | "wifi"
                                            │
                                     InterfaceManager
                                     device name ──► local IP (e.g. 192.168.8.31)
                                            │
                                     net.Dialer{LocalAddr: 192.168.8.31:0}
                                            │
                       DNS ─────────────────┤   ⚠ resolved by the system resolver,
                       (system, unbound)    │     NOT bound to the chosen interface
                                            ▼
                                     bind(2) ──► connect(2) ──►  upstream
                                            │
                       relay ◄───────────── established conn
```

The binding is **source-address binding, not device binding**. `internal/network/dialer.go` builds a
`net.Dialer` with `LocalAddr` set to the first IPv4 address of the selected interface and lets the
stdlib call `bind(2)` before `connect(2)`. There is no `net.Dialer.Control` hook, no `IP_BOUND_IF`
(macOS) and no `SO_BINDTODEVICE` (Linux) — the code path is byte-identical on both platforms. The
practical difference is in the kernel:

- **macOS** enables scoped routing by default (`net.inet.ip.scopedroute`), and keeps a per-interface
  route scope. A socket bound to a secondary interface's address is routed out that interface. This
  is why the mechanism works out of the box on macOS, unprivileged.
- **Linux** selects the route from the destination and the rule tables; the bound source address does
  not by itself select an egress interface. Packets will normally leave via the default route
  carrying a foreign source address and be dropped by reverse-path filtering or by the upstream ISP.
  To make splitdial effective on Linux you need a matching source-based rule per interface, e.g.
  `ip rule add from 192.168.8.31 lookup 100` with a default route in table 100. Doing this properly
  device-side would require `SO_BINDTODEVICE`, which needs `CAP_NET_RAW` — that is the trade splitdial
  declines to make.

If the selected interface has no usable address, the dial **falls back to the system default route**
with a warning rather than failing (`dialer.go:34`). That is a deliberate availability choice and a
leak you should know about.

## Quick start

```bash
git clone https://github.com/waylen888/splitdial.git && cd splitdial
cp config.example.yaml config.yaml
networksetup -listallhardwareports          # macOS: find your hardware port names
$EDITOR config.yaml                         # set interfaces.cable / interfaces.wifi
go build -o bin/proxy ./cmd/proxy && ./bin/proxy -config config.yaml
```

With a rule routing `ifconfig.me` to `wifi` and a default route of `cable`, the proof is two
different public IPs from the same machine, at the same moment:

```console
$ curl -s https://ifconfig.me                                     # system default route
203.0.113.7
$ curl -s --proxy socks5h://127.0.0.1:1080 https://ifconfig.me    # rule -> wifi
198.51.100.42
```

Use `socks5h://`, not `socks5://` — with `socks5h` curl passes the hostname to splitdial, so domain
rules can match. With `socks5://` curl resolves locally and splitdial only ever sees an IP.

## Configuration reference

Config path comes from `-config` (default `config.yaml`). Relative paths are searched next to the
executable, then one and two directories up, then the working directory. A missing file is not an
error — the built-in defaults are used.

| Field | Type | Default | Description |
|---|---|---|---|
| `server.socks_addr` | string | `127.0.0.1:1080` | SOCKS5 listener. |
| `server.http_addr` | string | `127.0.0.1:8080` | HTTP proxy listener. |
| `server.api_addr` | string | `127.0.0.1:8081` | REST API listener. |
| `interfaces.cable.device` | string | `en0` | Device name, e.g. `en7`, `eth0`. Takes precedence over `hardware_port`. |
| `interfaces.cable.hardware_port` | string | — | macOS hardware port name, resolved via `networksetup -listallhardwareports`. Stable across reboots; macOS only. |
| `interfaces.wifi.device` | string | `en1` | As above, for the `wifi` slot. |
| `interfaces.wifi.hardware_port` | string | — | As above. |
| `logging.level` | string | `info` | `debug` \| `info` \| `warn` \| `error`. Hot-reloadable. |
| `logging.format` | string | `text` | `text` \| `json` (slog handlers). |
| `logging.output` | string | `stdout` | `stdout`, `stderr`, or a file path. `~` is expanded; parent dirs are created. |
| `logging.max_size` | int | `100` | MB before rotation. File output only. |
| `logging.max_backups` | int | `3` | Rotated files retained. |
| `logging.max_age` | int | `28` | Days retained. |
| `logging.compress` | bool | `true` | gzip rotated files. |
| `routes[].id` | string | — | Rule identifier; required by the REST API. |
| `routes[].name` | string | — | Human label, appears in logs. |
| `routes[].interface` | string | — | `cable` or `wifi`. No other values exist. |
| `routes[].enabled` | bool | `false` | **Must be set explicitly.** An omitted `enabled` means the rule is skipped. |
| `routes[].match.domains` | []string | — | Exact, `*.suffix`, or glob (`filepath.Match`) patterns. Case-insensitive. |
| `routes[].match.ips` | []string | — | CIDR (`10.0.0.0/8`) or single IP. |
| `routes[].match.ports` | []int | — | Destination port numbers. |
| `routes[].match.protocol` | string | — | Parsed but **never evaluated**. Has no effect. |

Only two interface slots exist — `cable` and `wifi`. They are labels, not detections: point `cable`
at a Wi-Fi adapter if you like. Interface names are resolved once at startup.

## Rule matching semantics

- **First enabled match wins**, in file order. There is no specificity ranking.
- `enabled: false` rules are skipped entirely.
- A rule whose `match` has no `domains`, `ips` *and* `ports` is a catch-all — `match: {}` is not
  special-cased, it simply matches everything. Put it last.
- If no rule matches, the fallback is hardcoded to `cable`.
- Within a rule, each populated category must match (AND). Within a category, any entry matches (OR).
- Matching runs on the **literal host string the client sent**, before DNS. A client sending a
  hostname cannot match an `ips` rule, and a client sending an IP cannot match a `domains` rule.
- `*.example.com` matches `example.com` and any subdomain. Patterns are suffix/glob based; there is no
  leftmost-label-only restriction, so `*.example.com` also matches `a.b.example.com`.

```yaml
routes:
  - id: media                       # 1. matches app.netflix.com  -> wifi
    match: {domains: ["*.netflix.com"], ports: [443]}
    interface: wifi
    enabled: true

  - id: lan                         # 2. matches 10.2.3.4         -> cable
    match: {ips: ["10.0.0.0/8"]}    #    does NOT match "intranet.corp" (host is a name)
    interface: cable
    enabled: true

  - id: default                     # 3. matches everything else  -> cable
    match: {}
    interface: cable
    enabled: true
```

`netflix.com:80` falls through rule 1 (port mismatch, AND semantics) and lands on rule 3.

## REST API

Listens on `api_addr`, no authentication, `Access-Control-Allow-Origin: *`. Bind it to loopback.

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/status` | Listener addresses and rule count. |
| `GET` | `/api/interfaces` | All non-loopback interfaces with at least one IP: name, type, IPv4/IPv6 addresses, MAC, up flag, MTU. |
| `GET` | `/api/rules` | All rules, in evaluation order. |
| `POST` | `/api/rules` | Append a rule (JSON `RouteRule`, `id` required). Applies live, then rewrites the config file. `201`. |
| `GET` | `/api/rules/{id}` | Single rule, or `404`. |
| `DELETE` | `/api/rules/{id}` | Remove by id. `204`, or `404`. |
| `GET` | `/api/config` | Full effective config. |
| `PUT` | `/api/config` | Accepts a full `Config` body but **applies only `routes`**; server, interface and logging sections are ignored. |

```console
$ curl -s http://127.0.0.1:8081/api/status
{"api_addr":"127.0.0.1:8081","http_addr":"127.0.0.1:8080","rules":4,"running":true,"socks_addr":"127.0.0.1:1080"}

$ curl -s -XPOST http://127.0.0.1:8081/api/rules -d '
  {"id":"gh","name":"GitHub","match":{"domains":["*.github.com"]},"interface":"wifi","enabled":true}'
```

Note that any write endpoint re-serialises the whole config to YAML — **comments and formatting in
your config file are lost**.

## Hot reload

`internal/config/config.go` watches the config file's *directory* with fsnotify (so atomic
rename-into-place editors work) and reloads on write/create of the matching basename, debounced 100ms.
On reload it re-applies exactly two things: **routing rules** and **log level**. Listener addresses,
interface specifications and the remaining logging options are read once at startup and require a
restart. Established connections are never touched; new connections use the new rules.

## Running as a service

`./scripts/install_service.sh` builds and installs to `~/.local/bin/splitdial-proxy`, config to
`~/.config/splitdial/config.yaml`, logs to `~/.local/state/splitdial/proxy.log`, then registers a
macOS LaunchAgent (`com.waylen.splitdial`) or a Linux systemd **user** unit (`splitdial`) with
start-at-login and restart-on-failure.

```bash
# macOS
launchctl unload ~/Library/LaunchAgents/com.waylen.splitdial.plist
launchctl load   ~/Library/LaunchAgents/com.waylen.splitdial.plist
tail -f ~/.local/state/splitdial/proxy.log

# Linux
systemctl --user restart splitdial
journalctl --user -u splitdial -f
```

The script seeds the installed config from `debug-data/config.yaml` if present, otherwise from
`config.example.yaml`.

## Limitations & known issues

- **DNS is not interface-bound.** Name resolution happens inside `net.Dialer.DialContext` using the
  system resolver over the default route. Rule selection and DNS therefore disagree: you get the
  answer your default uplink's resolver gives, then connect to it from the other interface. Expect
  wrong answers from split-horizon DNS and CDN geo-steering, and treat this as a DNS leak.
- **Linux needs source-based policy routing** to be effective. See *How it works*.
- **TCP only.** SOCKS5 `BIND` (0x02) and `UDP ASSOCIATE` (0x03) are rejected with
  `0x07 Command not supported`. QUIC/HTTP-3 and DNS-over-UDP cannot traverse the proxy.
- **No authentication**, on either proxy or the REST API. Only SOCKS5 method `0x00` (no auth) is
  offered. Bind everything to loopback.
- **IPv6 is only reachable for literal IPv6 destinations.** The local address family is chosen by
  parsing the *target string*; a hostname never parses as an IPv6 literal, so a hostname target always
  binds the interface's IPv4 address and the connection is forced to IPv4. Link-local-only interfaces
  are rejected for IPv6 targets.
- **Silent fallback on interface loss.** If the target interface is down or has no address at dial
  time, the connection is made over the default route and only a warning is logged.
- **Established connections are not re-homed.** If the interface goes down mid-connection, the
  connection dies as it normally would; splitdial does not migrate or retry it.
- **Per-destination only, not per-app.** Routing is decided from destination host/port. The only way
  to give an application its own route is to point that application at the proxy.
- **HTTP proxy handles one request per connection.** After forwarding a plain (non-CONNECT) request it
  copies the response and closes; there is no keep-alive reuse and hop-by-hop headers are not stripped.
- **Exactly two interface slots** (`cable`, `wifi`). Three uplinks are not expressible.
- `match.protocol` is accepted by the parser and ignored by the matcher.

## License

MIT — see [LICENSE](LICENSE).
