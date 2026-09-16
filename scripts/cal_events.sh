#!/bin/sh
# Upcoming calendar events as a table: tab-separated with a header line → type: table.
# Optional argument: the window in days, default 7. At most MAX_ROWS rows are printed, so a
# crowded window cannot outgrow the panel.
#
# Duplicates are merged. A calendar subscribed twice is listed twice in Calendar.app, and every
# one of its events then comes out twice; icalBuddy has no dedup of its own. Rows are collapsed
# only when they match throughout, calendar name included, so one title kept in two different
# calendars legitimately stays two rows.
#
# Platform dependency: macOS with icalBuddy (brew install ical-buddy). icalBuddy reads the same
# local calendar store Calendar.app uses, which is why it is preferred here over AppleScript:
# one query answers in well under a second, where AppleScript needs about ten for a single day.
#
# -ps reads its first and last character as the separator marker and everything between them as
# the component, so a tab cannot be marker and component at once — "|" marks the position and
# the tab is the component.
set -u

DAYS="${1:-7}"
MAX_ROWS=10

printf 'time\tevent\n'

command -v icalBuddy >/dev/null 2>&1 || {
    echo "cal_events.sh: icalBuddy not found (brew install ical-buddy)" >&2
    exit 1
}

# icalBuddy's own failure is left on stderr, so the panel's footer says why rather than showing
# a table that is merely empty.
icalBuddy \
    -eep notes,url,attendees \
    -po datetime,title \
    -ps "$(printf '|\t|')" \
    -b '' \
    -nrd \
    -df '%m-%d' -tf '%H:%M' \
    eventsFrom:today "to:today+${DAYS}days" |
    awk -F'\t' -v OFS='\t' '
        NF {
            # icalBuddy writes " at " between the date and the time; the field holds nothing but
            # timing information, so dropping it tightens the column.
            sub(/ at /, " ", $1)
            if (!seen[$0]++) print
        }
    ' |
    head -n "$MAX_ROWS"
