#!/usr/bin/env bash
# The closet camera's detector: render its config and start or stop it
# (ROADMAP §2.1). One script for development and for an install, like
# deploy/stockroom-run.sh, so there is one definition of "the camera stack".
#
#   camera.sh up [--source webcam|file:PATH] [--reconfigure]
#   camera.sh down | status | logs
#
# What runs:
#
#   Linux, webcam   Frigate, with the USB device mapped into the container and
#                   Frigate's own go2rtc turning its MJPEG into H.264.
#   macOS, webcam   go2rtc on the HOST reading the webcam through AVFoundation
#                   (Docker Desktop cannot pass a USB device into a container),
#                   encoding with VideoToolbox, and serving RTSP on
#                   127.0.0.1:8554; Frigate reads that.
#   file:PATH       Frigate reading a video file on a loop. How the detector is
#                   tested without a camera, and how CI-free "sample footage"
#                   checks are run on a development machine.
#
# Environment (all optional):
#   CAMERA_DIR          where Frigate's config and media live
#                       (default: $HOME/.stockroom-camera)
#   CAMERA_DEVICE       Linux webcam device (default /dev/video0)
#   CAMERA_INDEX        macOS AVFoundation video index (default 0; list them
#                       with: ffmpeg -f avfoundation -list_devices true -i "")
#   CAMERA_SIZE         capture size (default 960x720, the QuickCam Pro 9000's
#                       largest MJPEG mode; the FaceTime camera wants 1280x720)
#   CAMERA_FPS          capture and record rate (default 15)
#   CAMERA_DETECTOR     cpu | openvino (default: openvino on an Intel Linux
#                       machine, cpu everywhere else)
#   GO2RTC              the go2rtc binary on macOS (default: go2rtc on PATH,
#                       then ~/.local/bin/go2rtc)
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CAMERA_DIR="${CAMERA_DIR:-$HOME/.stockroom-camera}"
export CAMERA_DIR
OS="$(uname -s)"
GO2RTC_PLIST="$HOME/Library/LaunchAgents/com.stockroom.go2rtc.plist"

log() { echo "[camera] $*"; }
die() { echo "[camera] ERROR: $*" >&2; exit 1; }

compose() {
  local files=(-f "$HERE/docker-compose.yml")
  if [ "$OS" = "Linux" ] && [ -f "$CAMERA_DIR/.linux-webcam" ]; then
    files+=(-f "$HERE/compose.linux-webcam.yml")
  fi
  (cd "$HERE" && docker compose "${files[@]}" "$@")
}

go2rtc_bin() {
  if [ -n "${GO2RTC:-}" ]; then echo "$GO2RTC"; return; fi
  if command -v go2rtc >/dev/null 2>&1; then command -v go2rtc; return; fi
  if [ -x "$HOME/.local/bin/go2rtc" ]; then echo "$HOME/.local/bin/go2rtc"; return; fi
  echo ""
}

detector_block() {
  local det="${CAMERA_DETECTOR:-}"
  if [ -z "$det" ]; then
    det=cpu
    if [ "$OS" = "Linux" ] && [ "$(uname -m)" = "x86_64" ] && grep -qi 'GenuineIntel' /proc/cpuinfo 2>/dev/null; then
      det=openvino
    fi
  fi
  case "$det" in
    openvino)
      # Frigate's bundled OpenVINO SSDLite model: runs on the CPU or the
      # integrated GPU with no graphics card (ROADMAP §2.1, "CPU-only works").
      cat <<'YML'
detectors:
  ov:
    type: openvino
    device: AUTO
model:
  width: 300
  height: 300
  input_tensor: nhwc
  input_pixel_format: bgr
  path: /openvino-model/ssdlite_mobilenet_v2.xml
  labelmap_path: /openvino-model/coco_91cl_bkgr.txt
YML
      ;;
    cpu)
      cat <<'YML'
detectors:
  cpu1:
    type: cpu
    num_threads: 2
YML
      ;;
    *) die "CAMERA_DETECTOR must be cpu or openvino, not '$det'" ;;
  esac
}

# render_config SOURCE writes $CAMERA_DIR/config/config.yml. The detection and
# recording numbers are the ROADMAP §2.1 tuning for a low-end closet PC:
# detect a 640-wide downscale at 5 fps, record only while a person is in view
# plus five seconds either side, and keep Frigate's own copy for three days --
# Stockroom copies each visit's clip into its own recordings folder and owns
# the real retention (Admin -> Settings), so Frigate's is only a buffer.
render_config() {
  local source="$1" size="${CAMERA_SIZE:-960x720}" fps="${CAMERA_FPS:-15}"
  local w="${size%x*}" h="${size#*x}"
  local input input_args go2rtc_block="" detect_w=640 detect_h

  case "$source" in
    file:*)
      local f="${source#file:}"
      [ -f "$f" ] || die "no such file: $f"
      f="$(cd "$(dirname "$f")" && pwd)/$(basename "$f")"
      echo "$(dirname "$f")" > "$CAMERA_DIR/.sample-dir"
      if command -v ffprobe >/dev/null 2>&1; then
        local dims
        dims="$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0:s=x "$f" || true)"
        [ -n "$dims" ] && w="${dims%x*}" && h="${dims#*x}"
      fi
      input="/media/sample/$(basename "$f")"
      # Loop forever at real speed, as a camera would deliver it.
      input_args="-re -stream_loop -1 -fflags +genpts"
      ;;
    webcam)
      rm -f "$CAMERA_DIR/.sample-dir"
      if [ "$OS" = "Darwin" ]; then
        input="rtsp://host.docker.internal:8554/closet"
        input_args="preset-rtsp-generic"
      else
        : > "$CAMERA_DIR/.linux-webcam"
        local dev="${CAMERA_DEVICE:-/dev/video0}"
        # Frigate's go2rtc reads the webcam's MJPEG and encodes H.264 once,
        # which both detect and record then share. #hardware uses Quick Sync
        # where the machine has it and falls back to software otherwise.
        go2rtc_block="go2rtc:
  streams:
    closet:
      - \"ffmpeg:device?video=${dev}&input_format=mjpeg&video_size=${size}&framerate=${fps}#video=h264#hardware\""
        input="rtsp://127.0.0.1:8554/closet"
        input_args="preset-rtsp-restream"
      fi
      ;;
    *) die "--source must be webcam or file:PATH" ;;
  esac
  detect_h=$(( (detect_w * h / w) / 2 * 2 ))

  mkdir -p "$CAMERA_DIR/config" "$CAMERA_DIR/media"
  {
    cat <<YML
# Rendered by deploy/camera/camera.sh for source: $source
# Re-render with: camera.sh up --reconfigure
mqtt:
  enabled: false
auth:
  enabled: false
telemetry:
  version_check: false
$(detector_block)
$go2rtc_block
objects:
  track: [person]
  filters:
    person:
      min_score: 0.5
      threshold: 0.7
record:
  enabled: true
  continuous:
    days: 0
  motion:
    days: 0
  alerts:
    pre_capture: 5
    post_capture: 5
    retain:
      days: 3
  detections:
    pre_capture: 5
    post_capture: 5
    retain:
      days: 3
snapshots:
  enabled: true
  bounding_box: true
  retain:
    default: 3
review:
  alerts:
    labels: [person]
cameras:
  closet:
    ffmpeg:
      inputs:
        - path: $input
          input_args: $input_args
          roles: [detect, record]
    detect:
      enabled: true
      width: $detect_w
      height: $detect_h
      fps: 5
YML
  } > "$CAMERA_DIR/config/config.yml"
  echo "$source" > "$CAMERA_DIR/.source"
  log "wrote $CAMERA_DIR/config/config.yml ($source)"
}

start_go2rtc() {
  local bin; bin="$(go2rtc_bin)"
  [ -n "$bin" ] || die "go2rtc is not installed. Download go2rtc_mac_arm64.zip (or _amd64) from https://github.com/AlexxIT/go2rtc/releases into ~/.local/bin"
  command -v ffmpeg >/dev/null 2>&1 || die "ffmpeg is not installed (brew install ffmpeg)"
  local size="${CAMERA_SIZE:-960x720}" fps="${CAMERA_FPS:-15}" idx="${CAMERA_INDEX:-0}"
  cat > "$CAMERA_DIR/go2rtc.yaml" <<YML
# Rendered by deploy/camera/camera.sh. go2rtc on the macOS host: the webcam
# through AVFoundation, H.264 by VideoToolbox, RTSP on loopback only.
api:
  listen: "127.0.0.1:1984"
rtsp:
  listen: "127.0.0.1:8554"
webrtc:
  listen: ""
streams:
  closet:
    - "ffmpeg:device?video=${idx}&video_size=${size}&framerate=${fps}#video=h264#hardware"
YML
  stop_go2rtc
  # An install runs go2rtc as a launchd agent, which restarts it and starts
  # it at login (scripts/install.sh --with-camera). Development runs it here.
  if [ -f "$GO2RTC_PLIST" ]; then
    launchctl kickstart -k "gui/$(id -u)/com.stockroom.go2rtc" >/dev/null 2>&1 \
      || launchctl bootstrap "gui/$(id -u)" "$GO2RTC_PLIST"
    log "go2rtc restarted by launchd, log: $CAMERA_DIR/go2rtc.log"
  else
    nohup "$bin" -config "$CAMERA_DIR/go2rtc.yaml" > "$CAMERA_DIR/go2rtc.log" 2>&1 &
    echo $! > "$CAMERA_DIR/go2rtc.pid"
    log "go2rtc started (pid $(cat "$CAMERA_DIR/go2rtc.pid")), log: $CAMERA_DIR/go2rtc.log"
  fi
  log "macOS asks once for camera access for the app that runs this script; allow it in System Settings -> Privacy & Security -> Camera"
}

stop_go2rtc() {
  if [ -f "$GO2RTC_PLIST" ] && [ "${1:-}" = "all" ]; then
    launchctl bootout "gui/$(id -u)" "$GO2RTC_PLIST" 2>/dev/null || true
  fi
  if [ -f "$CAMERA_DIR/go2rtc.pid" ]; then
    kill "$(cat "$CAMERA_DIR/go2rtc.pid")" 2>/dev/null || true
    rm -f "$CAMERA_DIR/go2rtc.pid"
  fi
}

cmd="${1:-status}"; shift || true
case "$cmd" in
  up)
    source="" reconfigure=0
    while [ $# -gt 0 ]; do
      case "$1" in
        --source) source="$2"; shift 2 ;;
        --source=*) source="${1#*=}"; shift ;;
        --reconfigure) reconfigure=1; shift ;;
        *) die "unknown option $1" ;;
      esac
    done
    mkdir -p "$CAMERA_DIR"
    [ -z "$source" ] && [ -f "$CAMERA_DIR/.source" ] && source="$(cat "$CAMERA_DIR/.source")"
    source="${source:-webcam}"
    if [ "$reconfigure" = 1 ] || [ ! -f "$CAMERA_DIR/config/config.yml" ] || [ "$(cat "$CAMERA_DIR/.source" 2>/dev/null)" != "$source" ]; then
      render_config "$source"
    fi
    if [ -f "$CAMERA_DIR/.sample-dir" ]; then
      export CAMERA_SAMPLE_DIR; CAMERA_SAMPLE_DIR="$(cat "$CAMERA_DIR/.sample-dir")"
    fi
    if [ "$OS" = "Darwin" ] && [ "$source" = "webcam" ]; then start_go2rtc; else stop_go2rtc; fi
    compose up -d
    log "Frigate is starting on http://127.0.0.1:5055 (first start takes a minute)"
    log "then turn the camera on in Stockroom: Admin -> Settings -> Closet camera"
    ;;
  down)
    stop_go2rtc all
    compose down
    ;;
  status)
    compose ps
    if curl -fsS --max-time 3 http://127.0.0.1:5055/api/version >/dev/null 2>&1; then
      log "Frigate $(curl -fsS http://127.0.0.1:5055/api/version) is answering"
    else
      log "Frigate is not answering on 127.0.0.1:5055"
    fi
    ;;
  logs) compose logs --tail 100 -f ;;
  *) die "usage: camera.sh up [--source webcam|file:PATH] [--reconfigure] | down | status | logs" ;;
esac
