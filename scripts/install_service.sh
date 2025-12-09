#!/bin/bash
set -e

# Configuration settings
APP_NAME="com.waylen.splitdial"
BIN_NAME="splitdial-proxy"
SERVICE_NAME="splitdial" # For systemd
SOURCE_CONFIG="debug-data/config.yaml"

# Determine OS and paths
OS="$(uname -s)"
case "$OS" in
    Darwin*)
        echo "🍎 Detected macOS"
        INSTALL_BIN_DIR="$HOME/.local/bin"
        INSTALL_CONFIG_DIR="$HOME/.config/splitdial"
        INSTALL_LOG_DIR="$HOME/.local/state/splitdial"
        PLIST_PATH="$HOME/Library/LaunchAgents/$APP_NAME.plist"
        ;;
    Linux*)
        echo "🐧 Detected Linux"
        INSTALL_BIN_DIR="$HOME/.local/bin"
        INSTALL_CONFIG_DIR="$HOME/.config/splitdial"
        INSTALL_LOG_DIR="$HOME/.local/state/splitdial"
        SYSTEMD_DIR="$HOME/.config/systemd/user"
        SERVICE_PATH="$SYSTEMD_DIR/$SERVICE_NAME.service"
        ;;
    *)
        echo "❌ Unsupported OS: $OS"
        exit 1
        ;;
esac

TARGET_BIN="$INSTALL_BIN_DIR/$BIN_NAME"
TARGET_CONFIG="$INSTALL_CONFIG_DIR/config.yaml"
LOG_FILE="$INSTALL_LOG_DIR/proxy.log"

echo "=== Installing Splitdial Proxy Service ==="

# 1. Prepare directories
echo "📂 Creating directories..."
mkdir -p "$INSTALL_BIN_DIR"
mkdir -p "$INSTALL_CONFIG_DIR"
mkdir -p "$INSTALL_LOG_DIR"

if [ "$OS" = "Linux" ]; then
    mkdir -p "$SYSTEMD_DIR"
fi

# 2. Build and Install Binary
echo "🔨 Building binary..."
if go build -o "$BIN_NAME" ./cmd/proxy; then
    echo "📦 Installing binary to $TARGET_BIN..."
    mv "$BIN_NAME" "$TARGET_BIN"
    chmod +x "$TARGET_BIN"
else
    echo "❌ Build failed!"
    exit 1
fi

# 3. Install Config
if [ -f "$SOURCE_CONFIG" ]; then
    if [ -f "$TARGET_CONFIG" ]; then
        cp "$TARGET_CONFIG" "${TARGET_CONFIG}.bak"
        echo "Example: Backed up existing config to ${TARGET_CONFIG}.bak"
    fi
    echo "⚙️  Installing config from $SOURCE_CONFIG to $TARGET_CONFIG..."
    cp "$SOURCE_CONFIG" "$TARGET_CONFIG"
else
    echo "⚠️  Warning: Source config $SOURCE_CONFIG not found."
    if [ ! -f "$TARGET_CONFIG" ]; then
        echo "Creating default config from example..."
        cp config.example.yaml "$TARGET_CONFIG"
    else
        echo "Using existing config at $TARGET_CONFIG"
    fi
fi

# 4. Create Service Definition & Start
if [[ "$OS" == "Darwin"* ]]; then
    # macOS LaunchAgent
    echo "📝 Creating LaunchAgent plist at $PLIST_PATH..."
    cat > "$PLIST_PATH" << EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>$APP_NAME</string>
    <key>ProgramArguments</key>
    <array>
        <string>$TARGET_BIN</string>
        <string>-config</string>
        <string>$TARGET_CONFIG</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>$LOG_FILE</string>
    <key>StandardErrorPath</key>
    <string>$LOG_FILE</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
    </dict>
    <key>WorkingDirectory</key>
    <string>$INSTALL_CONFIG_DIR</string>
</dict>
</plist>
EOF

    echo "🚀 Reloading LaunchAgent..."
    launchctl unload "$PLIST_PATH" 2>/dev/null || true
    launchctl load "$PLIST_PATH"
    
    echo "✅ Installation Complete!"
    sleep 1
    if launchctl list | grep -q "$APP_NAME"; then
        STATUS=$(launchctl list | grep "$APP_NAME")
        echo "Status: Running ($STATUS)"
    else
        echo "⚠️  Warning: Service does not appear to be running."
    fi

elif [[ "$OS" == "Linux"* ]]; then
    # Linux Systemd (User)
    echo "📝 Creating systemd unit at $SERVICE_PATH..."
    cat > "$SERVICE_PATH" << EOF
[Unit]
Description=Splitdial Proxy Service
After=network.target

[Service]
Type=simple
ExecStart=$TARGET_BIN -config $TARGET_CONFIG
WorkingDirectory=$INSTALL_CONFIG_DIR
StandardOutput=append:$LOG_FILE
StandardError=append:$LOG_FILE
Restart=always
RestartSec=3

[Install]
WantedBy=default.target
EOF

    echo "🚀 Reloading systemd..."
    systemctl --user daemon-reload
    systemctl --user enable "$SERVICE_NAME"
    systemctl --user restart "$SERVICE_NAME"

    echo "✅ Installation Complete!"
    sleep 1
    if systemctl --user is-active --quiet "$SERVICE_NAME"; then
        echo "Status: Running"
        systemctl --user status "$SERVICE_NAME" --no-pager | head -n 3
    else
        echo "⚠️  Warning: Service does not appear to be running."
        systemctl --user status "$SERVICE_NAME" --no-pager
    fi
fi

echo ""
echo "   Binary: $TARGET_BIN"
echo "   Config: $TARGET_CONFIG"
echo "   Logs:   $LOG_FILE"

