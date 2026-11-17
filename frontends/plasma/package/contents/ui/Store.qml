// Pure presentation logic: parsing `bingofuel fetch --json`, colours, text
// and layout maths. It imports no Plasma module, so qmltestrunner can test it.
import QtQuick

QtObject {
    id: store

    // --- inputs (bound from plasmoid configuration by main.qml) ---
    property int warningThreshold: 80
    property int criticalThreshold: 95
    property color warningColor: "#E5C07B"
    property color criticalColor: "#E06C75"
    property var providerColors: ({})      // { claude: "#D77757", ... }
    property bool lightTheme: false
    property color textColor: "white"
    property string currencySymbol: "$"
    property string gaugeLabel: "percent"  // percent | time
    property real nowMs: Date.now()

    // --- state ---
    property var groups: []
    property string error: ""
    property bool configFound: true
    property real fetchedAtMs: 0

    // Returns true when the output was usable. Malformed output keeps the
    // previous groups and sets `error`, so a hiccup never blanks the rings.
    function parse(text) {
        var data
        try {
            data = JSON.parse(text)
        } catch (e) {
            error = "bingofuel printed something that is not JSON"
            return false
        }
        if (!data || typeof data !== "object" || !Array.isArray(data.groups)) {
            error = "unexpected output from bingofuel"
            return false
        }
        error = data.error || ""
        configFound = data.config_found !== false
        fetchedAtMs = data.fetched_at ? new Date(data.fetched_at).getTime() : Date.now()
        groups = data.groups
        return true
    }

    function dimmed(group) {
        return !!group && group.status !== "ok" && group.status !== "rate_limited"
    }

    function groupColor(group) {
        var c = providerColors[group.id] || group.color || textColor
        // A white ring disappears on a light theme; fall back to the text colour.
        if (lightTheme && String(c).toLowerCase() === "#ffffff")
            return textColor
        return c
    }

    function ringColor(group, meter) {
        var p = meter ? meter.percent : 0
        if (p >= criticalThreshold) return criticalColor
        if (p >= warningThreshold) return warningColor
        return groupColor(group)
    }

    // Meters drawn as rings. A group with nothing to draw (not configured,
    // failed before any data) gets one placeholder so its heading still has
    // something to dim, and the reason shows on hover.
    function ringsFor(group) {
        var out = []
        var ms = group.meters || []
        for (var i = 0; i < ms.length; i++)
            if (ms[i].show) out.push(ms[i])
        if (out.length === 0)
            out.push({ id: "placeholder", kind: "placeholder", label: "", percent: 0 })
        return out
    }

    function formatMoney(v) {
        if (v === undefined || v === null || isNaN(v)) return "—"
        if (v >= 1000) return currencySymbol + Math.round(v)
        return currencySymbol + Number(v).toFixed(2)
    }

    // HH:MM under a day, "Nd Nh" from there; "" when there is no reset time.
    function formatCountdown(iso) {
        if (!iso) return ""
        var diff = new Date(iso).getTime() - nowMs
        if (isNaN(diff)) return ""
        if (diff <= 0) return "00:00"
        var mins = Math.floor(diff / 60000)
        var hrs = Math.floor(mins / 60)
        if (hrs >= 24) return Math.floor(hrs / 24) + "d " + (hrs % 24) + "h"
        var m = mins % 60
        return (hrs < 10 ? "0" : "") + hrs + ":" + (m < 10 ? "0" : "") + m
    }

    function valueText(meter) {
        if (!meter || meter.kind === "placeholder") return "—"
        if (meter.kind === "credit")
            return (meter.estimate ? "≈" : "") + formatMoney(meter.remaining)
        if (gaugeLabel === "time") {
            var t = formatCountdown(meter.resets_at)
            if (t !== "") return t
        }
        return Math.round(meter.percent) + "%"
    }

    // Second line: the window name. Credit rings show only their value.
    function nameText(meter) {
        if (!meter || meter.kind !== "window") return ""
        return meter.label || ""
    }

    // Past the warning threshold the name takes the ring's warning colour.
    function alert(meter) {
        return !!meter && meter.kind === "window" && meter.percent >= warningThreshold
    }

    // Third line: time to reset, unless the value line already shows it.
    function countdownText(meter) {
        if (!meter || meter.kind !== "window") return ""
        if (gaugeLabel === "time") return Math.round(meter.percent) + "%"
        return formatCountdown(meter.resets_at)
    }

    function ago(ms) {
        var d = nowMs - ms
        if (d < 60000) return "just now"
        if (d < 3600000) return Math.floor(d / 60000) + " min ago"
        if (d < 172800000) return (d / 3600000).toFixed(1) + " h ago"
        return Math.floor(d / 86400000) + " days ago"
    }

    function statusText(group) {
        if (!group || group.status === "ok") return ""
        var s = group.message || group.status
        if (group.last_success && dimmed(group))
            s += " (last good data " + ago(new Date(group.last_success).getTime()) + ")"
        return s
    }

    function tooltip(group, meter) {
        var head = group.title + (meter.label && meter.kind !== "placeholder" ? " " + meter.label : "")
        var lines = [head]
        if (meter.kind === "credit") {
            lines.push((meter.estimate ? "≈" : "") + formatMoney(meter.remaining) + " left of " + formatMoney(meter.total)
                       + " (" + Number(meter.percent).toFixed(1) + "% used)")
            if (meter.estimate) lines.push("Estimated from the balance you entered and logged calls")
        } else if (meter.kind === "window") {
            lines.push(Math.round(meter.percent) + "% used")
            if (meter.resets_at)
                lines.push("Resets " + Qt.formatDateTime(new Date(meter.resets_at), "ddd hh:mm"))
        }
        var st = statusText(group)
        if (st) lines.push(st)
        return lines.join("\n")
    }

    // --- layout ---
    // Geometry of one ring size s. Shared by fitRingSize and the views so
    // the simulation and the real Flow agree.
    function ringGap(s) { return Math.max(4, s * 0.14) }
    function groupGap(s) { return Math.max(8, s * 0.32) }
    // Label line plus a fixed gap between the label and its rings.
    readonly property int headingGap: 8
    function headingTextHeight(s) { return Math.max(12, s * 0.16) * 1.35 }
    function headingHeight(s, show) { return show ? headingTextHeight(s) + headingGap : 0 }

    // Largest ring size that fits `counts` (rings per group) into w x h.
    // mode: grid (groups flow and wrap), row (one line), column (one group per line).
    function fitRingSize(counts, w, h, mode, showHeadings) {
        if (!counts.length || w <= 0 || h <= 0) return 0
        function fits(s) {
            var rg = ringGap(s), gg = groupGap(s)
            var rowH = headingHeight(s, showHeadings) + s
            var lines = 1, x = 0
            for (var i = 0; i < counts.length; i++) {
                var gw = counts[i] * s + (counts[i] - 1) * rg
                if (gw > w) return false
                var need = x === 0 ? gw : x + gg + gw
                if (mode === "column" && x > 0) need = w + 1
                if (need > w) {
                    if (mode === "row") return false
                    lines++
                    x = gw
                } else {
                    x = need
                }
            }
            return lines * rowH + (lines - 1) * gg <= h
        }
        var lo = 8, hi = Math.max(w, h)
        if (!fits(lo)) return lo
        for (var k = 0; k < 30; k++) {
            var mid = (lo + hi) / 2
            if (fits(mid)) lo = mid; else hi = mid
        }
        return Math.floor(lo)
    }
}
