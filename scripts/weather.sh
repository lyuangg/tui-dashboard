#!/bin/bash
# Weather fetcher — pulls wttr.in's JSON into the "key: value" lines a map source wants.
# Numeric fields feed chart/stat; desc / icon / wind are strings, for text templates only.
#
#   city: Beijing
#   desc: Sunny
#   icon: ☀️
#   temp_c: 28
#   feels_like_c: 27
#   humidity: 26
#   wind: WSW 4km/h
#   precip_mm: 0.0
#   uv_index: 5
#   updated: 13:55
#
# Endpoint: https://wttr.in/<location>?format=j1 — free, no key, but slow and occasionally 5xx.
#   &m forces metric. weatherDesc in the JSON is always English.
#
# Env: WEATHER_LOCATION (default Beijing), WEATHER_CACHE_DIR, WEATHER_TTL (seconds, default 600).
# Cache: $HOME/.cache/tui-dashboard-weather/state. A failed fetch returns the cached value and
#   exits 0; with no cache it exits 1.
set -uo pipefail

LOCATION="${WEATHER_LOCATION:-Beijing}"
API_URL="https://wttr.in/${LOCATION}?format=j1&m"
CACHE_DIR="${WEATHER_CACHE_DIR:-$HOME/.cache/tui-dashboard-weather}"
CACHE_FILE="$CACHE_DIR/state"
TTL="${WEATHER_TTL:-600}"
mkdir -p "$CACHE_DIR" 2>/dev/null || true

# ── 0. TTL throttle: a fresh cache returns without touching the network ────────
if [ -f "$CACHE_FILE" ] && [ -s "$CACHE_FILE" ]; then
    MTIME="$(stat -f %m "$CACHE_FILE" 2>/dev/null || stat -c %Y "$CACHE_FILE" 2>/dev/null)"
    if [ -n "$MTIME" ] && [ $(( $(date +%s) - MTIME )) -lt "$TTL" ]; then
        cat "$CACHE_FILE"
        exit 0
    fi
fi

# Failure fallback: with a cache return the last result, otherwise exit 1 for the framework
fail() {
    echo "weather: $1" >&2
    if [ -s "$CACHE_FILE" ]; then
        cat "$CACHE_FILE"
        exit 0
    fi
    exit 1
}

TMP="$(mktemp)"
PARSER="$(mktemp)"
trap 'rm -f "$TMP" "$PARSER"' EXIT

# ── 1. Request ─────────────────────────────────────
CODE="$(curl -sS --max-time 15 -o "$TMP" -w '%{http_code}' \
    -H 'Accept: application/json' "$API_URL" 2>/dev/null)" || fail "curl failed: $API_URL"
case "$CODE" in
    2??) ;;
    *)   fail "HTTP ${CODE:-<empty>}" ;;
esac

# ── 2. Parse + format ─────────────────────────────
cat > "$PARSER" <<'PY'
import json, sys

try:
    d = json.load(open(sys.argv[1]))
    c = d["current_condition"][0]
    area = d["nearest_area"][0]["areaName"][0]["value"]
except Exception:
    sys.exit(1)

def num(key):
    try:
        return float(c.get(key))
    except (TypeError, ValueError):
        return None

def fmt(f):
    if f is None:
        return ""
    return f"{f:.0f}" if abs(f - round(f)) < 1e-9 else f"{f:.1f}"

def desc():
    w = c.get("weatherDesc")
    if isinstance(w, list) and w and isinstance(w[0], dict):
        return str(w[0].get("value") or "").strip()
    return ""

def wind():
    d, s = str(c.get("winddir16Point") or "").strip(), fmt(num("windspeedKmph"))
    if not s:
        return d
    return f"{d} {s}km/h".strip()

# weatherCode is WorldWeatherOnline's; grouped by class rather than listed code by code.
# Every emoji used is 2 columns wide, so changing the icon does not change the panel title's
# width and the border stays aligned.
DEFAULT_ICON = "🌡️"

def icon():
    try:
        code = int(c.get("weatherCode"))
    except (TypeError, ValueError):
        return DEFAULT_ICON
    if code == 113:
        return "☀️"                                     # clear
    if code == 116:
        return "⛅"                                      # partly cloudy
    if code in (119, 122):
        return "☁️"                                     # cloudy / overcast
    if code in (143, 248, 260):
        return "🌫️"                                    # fog
    if code in (200, 386, 389, 392, 395):
        return "⛈️"                                    # thunder
    if code in (176, 263, 266):
        return "🌦️"                                    # light showers / drizzle
    if code in (293, 296, 299, 302, 305, 308, 311, 314, 353, 356, 359):
        return "🌧️"                                    # rain
    if code in (179, 182, 185, 227, 230, 281, 284, 317, 320, 323, 326,
                329, 332, 335, 338, 350, 362, 365, 368, 371, 374, 377):
        return "🌨️"                                    # sleet / snow
    return DEFAULT_ICON                                 # an unlisted code gets the generic icon, not a guess

rows = [
    ("city", area),
    ("desc", desc()),
    ("icon", icon()),
    ("temp_c", fmt(num("temp_C"))),
    ("feels_like_c", fmt(num("FeelsLikeC"))),
    ("humidity", fmt(num("humidity"))),
    ("wind", wind()),
    ("precip_mm", fmt(num("precipMM"))),
    ("uv_index", fmt(num("uvIndex"))),
    ("updated", sys.argv[2]),
]

for k, v in rows:
    if v:                      # a missing field prints no line at all, so a template never reads ""
        print(f"{k}: {v}")
PY

OUT="$(python3 "$PARSER" "$TMP" "$(date '+%H:%M')" 2>/dev/null)"

if [ -z "$OUT" ]; then
    fail "parse failed — response shape changed?"
fi

mkdir -p "$CACHE_DIR" 2>/dev/null || true
echo "$OUT" > "$CACHE_FILE.tmp" && mv "$CACHE_FILE.tmp" "$CACHE_FILE"
echo "$OUT"
