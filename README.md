# Splitdial Proxy

Splitdial is a lightweight, rule-based proxy server capable of routing network traffic through specific network interfaces (e.g., Wi-Fi vs. Ethernet) based on configurable rules. It supports both SOCKS5 and HTTP proxy protocols and provides a REST API for dynamic management.

## Features

-   **Multi-Interface Routing**: Route traffic through specific network interfaces (e.g., `en0`, `en1`, `eth0`, `wlan0`).
-   **Dual Protocol Support**: Built-in SOCKS5 and HTTP proxy servers.
-   **Flexible Rules**: Route by domain (wildcards supported), IP address, or port.
-   **REST API**: Manage rules and check status dynamically.
-   **Cross-Platform Service**: Includes an installation script for macOS (LaunchAgent) and Linux (systemd).

## Installation

### Prerequisites

-   Go 1.21 or later

### Quick Install (macOS & Linux)

1.  Clone the repository:
    ```bash
    git clone https://github.com/waylen888/splitdial.git
    cd splitdial
    ```

2.  Run the installation script:
    ```bash
    chmod +x scripts/install_service.sh
    ./scripts/install_service.sh
    ```

    This script will:
    -   Build the binary and install it to `~/.local/bin/splitdial-proxy`.
    -   Install the configuration file to `~/.config/splitdial/config.yaml`.
    -   Set up a background service (LaunchAgent on macOS, systemd user service on Linux) to start automatically on login.

## Configuration

The configuration file is located at `~/.config/splitdial/config.yaml`.

### Network Interface Configuration

You can identify network interfaces in two ways:

**Option 1: Use `hardware_port` (Recommended)**

More stable - survives reboots and device reconnections. Find your hardware ports by running:

```bash
networksetup -listallhardwareports
```

**Option 2: Use `device` name directly**

Simpler, but may change after reboot (especially for USB adapters).

### Example `config.yaml`

```yaml
server:
  socks_addr: "127.0.0.1:1080"
  http_addr: "127.0.0.1:9090"
  api_addr: "127.0.0.1:9091"

# Using hardware_port (recommended - stable across reboots)
interfaces:
  cable:
    hardware_port: "USB 10/100/1000 LAN"  # or "Ethernet"
  wifi:
    hardware_port: "Wi-Fi"

# Or using device names directly (may change after reboot)
# interfaces:
#   cable:
#     device: "en7"
#   wifi:
#     device: "en0"

logging:
  level: "info"
  format: "text"
  output: "stdout"

routes:
  # Route streaming services via Wi-Fi
  - id: "streaming"
    name: "Streaming Services"
    match:
      domains:
        - "*.netflix.com"
        - "*.youtube.com"
    interface: "wifi"
    enabled: true

  # Route specific IP via Wi-Fi
  - id: "ssh-target"
    name: "SSH Server"
    match:
      ips:
        - "125.229.3.175"
    interface: "wifi"
    enabled: true

  # Default route
  - id: "default"
    name: "Default Route"
    match: {}
    interface: "cable"
    enabled: true
```

## Usage

Once installed, the service runs in the background. Configure your applications (browser, SSH, terminal) to use the proxy:

-   **SOCKS5 Proxy**: `127.0.0.1:1080`
-   **HTTP Proxy**: `127.0.0.1:9090`

### API

You can query the status of the proxy server:

```bash
curl http://127.0.0.1:9091/api/status
```

Response:
```json
{
  "api_addr": "127.0.0.1:9091",
  "http_addr": "127.0.0.1:9090",
  "rules": 3,
  "running": true,
  "socks_addr": "127.0.0.1:1080"
}
```

## Management

### macOS
-   **Restart Service**: `launchctl unload ~/Library/LaunchAgents/com.waylen.splitdial.plist && launchctl load ~/Library/LaunchAgents/com.waylen.splitdial.plist`
-   **View Logs**: `tail -f ~/.local/state/splitdial/proxy.log`

### Linux
-   **Restart Service**: `systemctl --user restart splitdial`
-   **View Logs**: `journalctl --user -u splitdial -f`

## License

MIT
