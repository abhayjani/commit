#!/usr/bin/env bash
# Installs Commit (Outscroll fork) as a macOS LaunchAgent so it starts on login
# and stays running in the background. Localhost-only, read-only, headless.
#
#   ./scripts/install-autostart.sh          # build (if needed), install, start
#   ./scripts/install-autostart.sh uninstall
set -euo pipefail

LABEL="com.outscroll.commit"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$REPO/commit-bin"
LOG="$HOME/.commit/commit.log"
export PATH="/opt/homebrew/bin:/usr/local/go/bin:$PATH"

if [[ "${1:-}" == "uninstall" ]]; then
  launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || launchctl unload "$PLIST" 2>/dev/null || true
  rm -f "$PLIST"
  echo "✓ Uninstalled. Commit will no longer auto-start (data in ~/.commit is untouched)."
  exit 0
fi

if [[ ! -x "$BIN" ]]; then
  echo "Building commit-bin..."
  ( cd "$REPO" && go build -o commit-bin . )
fi
mkdir -p "$HOME/.commit" "$HOME/Library/LaunchAgents"

cat > "$PLIST" <<PLISTEOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key>
  <array><string>$BIN</string></array>
  <key>EnvironmentVariables</key>
  <dict><key>COMMIT_HEADLESS</key><string>1</string></dict>
  <key>WorkingDirectory</key><string>$REPO</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$LOG</string>
  <key>StandardErrorPath</key><string>$LOG</string>
</dict>
</plist>
PLISTEOF

# Reload cleanly
launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || launchctl unload "$PLIST" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$PLIST" 2>/dev/null || launchctl load "$PLIST"

echo "✓ Installed and started. Commit now runs on login."
echo "  Dashboard : http://localhost:9384"
echo "  Logs      : $LOG"
echo "  Stop/remove: ./scripts/install-autostart.sh uninstall"
