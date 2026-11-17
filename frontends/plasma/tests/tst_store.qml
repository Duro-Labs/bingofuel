import QtQuick
import QtTest

import "../package/contents/ui"

TestCase {
    name: "Store"

    Store {
        id: store
        nowMs: new Date("2026-11-17T19:00:00Z").getTime()
        providerColors: ({ claude: "#D77757", codex: "#FFFFFF" })
        textColor: "#202020"
    }

    readonly property string sample: JSON.stringify({
        schema: 1, fetched_at: "2026-11-17T19:00:00Z", config_found: true,
        groups: [
            { id: "claude", title: "Claude", color: "#D77757", status: "ok", meters: [
                { id: "session", kind: "window", label: "5-Hour", show: true, percent: 47, resets_at: "2026-11-17T22:22:00Z" },
                { id: "weekly", kind: "window", label: "Weekly", show: true, percent: 85, resets_at: "2026-11-20T22:00:00Z" },
                { id: "weekly_opus", kind: "window", label: "Opus", show: false, percent: 12 } ] },
            { id: "openrouter", title: "OpenRouter", color: "#7AB8FF", status: "ok", meters: [
                { id: "credits", kind: "credit", label: "Credits", show: true, percent: 0.7, remaining: 9.927, total: 10, currency: "USD" } ] },
            { id: "typesafe", title: "JEV", color: "#D45BB6", status: "not_configured", message: "no JEV balance entered", meters: [] }
        ]
    })

    function init() {
        store.gaugeLabel = "percent"
        store.lightTheme = false
        verify(store.parse(sample))
    }

    function test_parse_keeps_previous_on_garbage() {
        compare(store.groups.length, 3)
        verify(!store.parse("Traceback: not json"))
        compare(store.groups.length, 3, "a bad run must not blank the rings")
        verify(store.error !== "")
        verify(!store.parse(JSON.stringify({ nope: 1 })))
    }

    function test_window_ring_text() {
        var s = store.groups[0].meters[0]
        compare(store.valueText(s), "47%")
        compare(store.nameText(s), "5-Hour")
        compare(store.countdownText(s), "03:22")
        verify(!store.alert(s))
        var w = store.groups[0].meters[1]
        compare(store.nameText(w), "Weekly", "the name never changes")
        verify(store.alert(w), "past the warning threshold the name is coloured")
        compare(store.countdownText(w), "3d 3h")
        verify(!store.alert(store.groups[1].meters[0]), "credit rings have no name line")
    }

    function test_time_mode_swaps_lines() {
        store.gaugeLabel = "time"
        var s = store.groups[0].meters[0]
        compare(store.valueText(s), "03:22")
        compare(store.countdownText(s), "47%")
    }

    function test_credit_ring_shows_only_remaining() {
        var c = store.groups[1].meters[0]
        compare(store.valueText(c), "$9.93")
        compare(store.nameText(c), "")
        compare(store.countdownText(c), "")
        c = JSON.parse(JSON.stringify(c)); c.estimate = true
        compare(store.valueText(c), "$9.93", "estimate status belongs in the tooltip, not the ring")
        verify(store.tooltip(store.groups[1], c).indexOf("Estimated") >= 0)
    }

    function test_rings_selection_and_placeholder() {
        compare(store.ringsFor(store.groups[0]).length, 2, "hidden meters are not rings")
        var p = store.ringsFor(store.groups[2])
        compare(p.length, 1)
        compare(p[0].kind, "placeholder")
        compare(store.valueText(p[0]), "—")
        verify(store.dimmed(store.groups[2]))
        verify(!store.dimmed(store.groups[0]))
    }

    function test_colours() {
        var g = store.groups[0]
        verify(Qt.colorEqual(store.ringColor(g, g.meters[0]), "#d77757"))
        verify(Qt.colorEqual(store.ringColor(g, g.meters[1]), store.warningColor))
        verify(Qt.colorEqual(store.ringColor(g, { percent: 99 }), store.criticalColor))
        var codex = { id: "codex", color: "#FFFFFF" }
        verify(Qt.colorEqual(store.groupColor(codex), "#ffffff"))
        store.lightTheme = true
        verify(Qt.colorEqual(store.groupColor(codex), "#202020"), "white falls back to text colour on light themes")
    }

    function test_countdown_edges() {
        compare(store.formatCountdown(""), "")
        compare(store.formatCountdown("2026-11-17T18:00:00Z"), "00:00")
        compare(store.formatCountdown("2026-11-18T19:00:00Z"), "1d 0h")
    }

    function test_fit_ring_size() {
        // Two groups of 2 rings each.
        var grid = store.fitRingSize([2, 2], 400, 400, "grid", true)
        var row = store.fitRingSize([2, 2], 400, 400, "row", true)
        var col = store.fitRingSize([2, 2], 400, 400, "column", true)
        verify(row > 0 && row < grid + 1, "a single row can't be bigger than the best grid")
        verify(col > 0)
        // Whatever size is chosen must fit the width.
        verify(2 * grid + store.ringGap(grid) <= 400)
        verify(4 * row + 2 * store.ringGap(row) + store.groupGap(row) <= 400)
        compare(store.fitRingSize([], 400, 400, "grid", true), 0)
    }

    function test_parse_balance() {
        compare(store.parseBalance("4.98"), 4.98)
        verify(isNaN(store.parseBalance("4,98")))
        verify(isNaN(store.parseBalance("1,000")), "must not become one dollar")
        compare(store.parseBalance("$5"), 5)
        verify(isNaN(store.parseBalance("1,000.50")), "thousands separators are refused")
        verify(isNaN(store.parseBalance("NaN")))
        verify(isNaN(store.parseBalance("")))
        verify(isNaN(store.parseBalance("-3")))
    }

    function test_next_reset() {
        // nowMs is 19:00Z; the Claude 5-Hour resets 22:22Z.
        compare(store.msUntilNextReset(), (3 * 60 + 22) * 60000)
    }
}
