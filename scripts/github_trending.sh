#!/bin/sh
# GitHub Trending — 抓 GitHub 官方 Trending 页(daily),按热度排序输出为制表符分隔的表
# Outputs: 首行表头 + 每行一条记录,制表符分隔 → type: table
#   rank  repo  language  today  description
#   1     morluto/rea  TypeScript  14927  Reverse engineer anything with agents...
#   today 列是该仓库「今日新增 star」,即 Trending 的原始排序依据;total stars 不冗余进表。
#
# GitHub 没有 Trending 的公共 API,故抓页面 HTML 交给 python3 解析(regex 取每个
# <article class="Box-row"> 块),无需认证;--compressed 让它收 gzip 响应,减轻带宽。
# since 可选 daily / weekly / monthly,默认 daily,用 GITHUB_TRENDING_SINCE 覆盖。
#
# 缓存: 结果持久化到 $HOME/.cache/tui-dashboard/github-trending/state,TTL 默认 3600 秒
#   (1 小时,GITHUB_TRENDING_TTL 覆盖)。面板再密的轮询都只命中缓存,真正抓页面最多
#   每小时一次;重启也由磁盘直接供数。
# 失败兜底: 有缓存则原样回上次结果并退 0(内容即上次抓取,一眼可辨新旧),无缓存则
#   退 1 交给框架保留上一值 —— 两种情况都不把面板清空。
#
# 平台依赖: curl, python3。
set -u

COUNT="${GITHUB_TRENDING_COUNT:-20}"
SINCE="${GITHUB_TRENDING_SINCE:-daily}"
URL="https://github.com/trending?since=${SINCE}"
CACHE_DIR="${GITHUB_TRENDING_CACHE_DIR:-$HOME/.cache/tui-dashboard/github-trending}"
CACHE_FILE="$CACHE_DIR/state"
TTL="${GITHUB_TRENDING_TTL:-3600}"
mkdir -p "$CACHE_DIR" 2>/dev/null || true

# ── 0. TTL 节流: 缓存新鲜则免网络直接返回 ────────
if [ -s "$CACHE_FILE" ]; then
    age=$(( $(date +%s) - $(stat -f %m "$CACHE_FILE" 2>/dev/null || stat -c %Y "$CACHE_FILE" 2>/dev/null) ))
    if [ -n "$age" ] && [ "$age" -lt "$TTL" ]; then
        cat "$CACHE_FILE"
        exit 0
    fi
fi

fail() {
    echo "github_trending: $1" >&2
    [ -s "$CACHE_FILE" ] && { cat "$CACHE_FILE"; exit 0; }
    exit 1
}

TMP="$(mktemp)"
PARSER="$(mktemp)"
trap 'rm -f "$TMP" "$PARSER"' EXIT

# ── 1. 请求页面 ─────────────────────────────────
CODE="$(curl -sSL --max-time 20 --compressed \
    -A 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36' \
    -o "$TMP" -w '%{http_code}' "$URL" 2>/dev/null)" || fail "curl failed: $URL"
case "$CODE" in
    2??) ;;
    429) fail "HTTP 429 rate-limited; try GITHUB_TRENDING_SINCE/weekly" ;;
    *)   fail "HTTP ${CODE:-<empty>}" ;;
esac

# ── 2. 解析 + 格式化 ─────────────────────────────
cat > "$PARSER" <<'PY'
import html, re, sys

try:
    page = open(sys.argv[1], encoding="utf-8").read()
except Exception:
    sys.exit(1)

n = int(sys.argv[2])
blocks = re.findall(r'<article[^>]*class="Box-row"[^>]*>(.*?)</article>', page, re.S)

def clean(s):
    return html.unescape(re.sub(r"<[^>]+>", "", s or "")).strip()

rows = []
for b in blocks[:n]:
    m = re.search(r'<h2[^>]*>\s*<a[^>]*href="/([^"/]+/[^"/]+)"', b)
    if not m:
        continue
    repo = m.group(1)
    m = re.search(r'itemprop="programmingLanguage"[^>]*>([^<]+)<', b)
    lang = clean(m.group(1)) if m else "-"
    m = re.search(r'<p[^>]*class="col-9[^"]*"[^>]*>\s*(.*?)\s*</p>', b, re.S)
    desc = clean(m.group(1)) if m else ""
    m = re.search(r'([\d,]+)\s+stars today', b)
    today = clean(m.group(1)).replace(",", "") if m else ""
    rows.append((repo, lang, today, desc))

print("rank\trepo\tlanguage\ttoday\tdescription")
for i, (repo, lang, today, desc) in enumerate(rows, 1):
    print(f"{i}\t{repo}\t{lang}\t{today}\t{desc}")
PY

OUT="$(python3 "$PARSER" "$TMP" "$COUNT" 2>/dev/null)"
if [ -z "$OUT" ]; then
    fail "parse failed — page structure changed?"
fi

echo "$OUT" > "$CACHE_FILE.tmp" && mv "$CACHE_FILE.tmp" "$CACHE_FILE"
echo "$OUT"