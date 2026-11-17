import QtQuick

import org.kde.kirigami as Kirigami
import org.kde.plasma.plasmoid
import org.kde.plasma.core as PlasmaCore
import org.kde.plasma.plasma5support as Plasma5Support

PlasmoidItem {
    id: root

    // The rings sit straight on the wallpaper; the popup paints its own card.
    Plasmoid.backgroundHints: PlasmaCore.Types.NoBackground
    preferredRepresentation: compactRepresentation

    readonly property var cfg: Plasmoid.configuration
    property bool busy: false
    property int failures: 0
    property real lastActivityMtime: 0

    Store {
        id: dataStore
        warningThreshold: root.cfg.warningThreshold
        criticalThreshold: root.cfg.criticalThreshold
        warningColor: root.cfg.warningColor
        criticalColor: root.cfg.criticalColor
        providerColors: ({
            claude: root.cfg.colorClaude,
            codex: root.cfg.colorCodex,
            openrouter: root.cfg.colorOpenrouter,
            typesafe: root.cfg.colorTypesafe
        })
        lightTheme: Kirigami.Theme.backgroundColor.hslLightness >= 0.5
        textColor: Kirigami.Theme.textColor
        currencySymbol: root.cfg.currencySymbol
        gaugeLabel: root.cfg.gaugeLabel
    }

    toolTipMainText: "Bingo Fuel"
    toolTipSubText: {
        var parts = []
        for (var i = 0; i < dataStore.groups.length; i++) {
            var g = dataStore.groups[i]
            var ms = dataStore.ringsFor(g).filter(m => m.kind !== "placeholder")
            parts.push(g.title + ": " + (ms.length ? ms.map(m => dataStore.valueText(m)).join(" · ") : (g.message || g.status)))
        }
        return parts.join("\n")
    }

    compactRepresentation: Rings {
        store: dataStore
        layoutMode: root.cfg.layoutMode
        showHeadings: root.cfg.showHeadings
        busy: root.busy
        onToggled: root.expanded = !root.expanded
    }

    fullRepresentation: Details {
        store: dataStore
        busy: root.busy
        backgroundAlpha: root.cfg.backgroundOpacity / 100
        onRefreshRequested: root.fetch(60)
        onJevBalanceEntered: usd => root.run(["jev", "balance", String(usd)], () => root.fetch(-1))
    }

    // --- running the core ---

    function shellQuote(s) {
        return "'" + String(s).replace(/'/g, "'\\''") + "'"
    }

    // The executable engine runs through a shell that inherits plasmashell's
    // environment, not a login shell's, so PATH is set explicitly.
    function command(args) {
        var bin = (root.cfg.binaryPath || "bingofuel").trim()
        if (bin.startsWith("~/")) bin = "$HOME/" + bin.slice(2)
        var quoted = bin.indexOf("$HOME/") === 0 ? "\"" + bin + "\"" : shellQuote(bin)
        return "PATH=\"$HOME/.local/bin:$HOME/go/bin:/usr/local/bin:/usr/bin:/bin:$PATH\" "
            + quoted + " " + args.map(shellQuote).join(" ")
    }

    property int nonce: 0
    property var callbacks: ({})

    Plasma5Support.DataSource {
        id: exec
        engine: "executable"
        connectedSources: []
        onNewData: (source, data) => {
            disconnectSource(source)
            var cb = root.callbacks[source]
            delete root.callbacks[source]
            if (cb) cb(data["exit code"], data["stdout"] || "", data["stderr"] || "")
        }
    }

    // Runs `bingofuel <args>`; each call gets a unique source name so two
    // identical commands never collapse into one.
    function run(args, cb) {
        var src = command(args) + " #" + (++nonce)
        callbacks[src] = cb
        exec.connectSource(src)
    }

    // maxAge < 0 uses the core's configured floor (5 minutes by default).
    function fetch(maxAge) {
        if (busy) return
        busy = true
        fetchTimeout.restart()
        var args = ["fetch"]
        if (maxAge >= 0) args.push("--max-age", String(maxAge))
        run(args, (code, out, err) => {
            busy = false
            fetchTimeout.stop()
            if (dataStore.parse(out)) {
                failures = 0
                pollTimer.interval = root.cfg.pollInterval * 1000
            } else {
                if (code === 127 || err.indexOf("not found") >= 0)
                    dataStore.error = "bingofuel was not found. Install it or set its path in the widget settings."
                failures++
                // Back off from one minute up to the poll interval.
                pollTimer.interval = Math.min(root.cfg.pollInterval * 1000, 60000 * Math.pow(2, failures - 1))
            }
            pollTimer.restart()
        })
    }

    Timer {
        id: fetchTimeout
        interval: 40000
        onTriggered: {
            for (var s in root.callbacks) exec.disconnectSource(s)
            root.callbacks = ({})
            root.busy = false
            dataStore.error = "bingofuel did not answer in 40 s"
        }
    }

    Timer {
        id: pollTimer
        interval: root.cfg.pollInterval * 1000
        repeat: true
        running: true
        onTriggered: root.fetch(-1)
    }

    // Keeps countdowns moving between fetches.
    Timer {
        interval: 30000
        repeat: true
        running: true
        onTriggered: dataStore.nowMs = Date.now()
    }

    // --- refresh soon after Claude or Codex is used ---

    Plasma5Support.DataSource {
        id: activity
        engine: "executable"
        connectedSources: []
        onNewData: (source, data) => {
            disconnectSource(source)
            var m = parseInt((data["stdout"] || "").trim())
            if (isNaN(m)) return
            if (root.lastActivityMtime > 0 && m > root.lastActivityMtime) activityDelay.restart()
            root.lastActivityMtime = m
        }
    }

    Timer {
        interval: Math.max(5, root.cfg.activityCheckInterval) * 1000
        repeat: true
        running: true
        onTriggered: activity.connectSource(
            "stat -c %Y \"${CLAUDE_CONFIG_DIR:-$HOME/.claude}/history.jsonl\" \"${CODEX_HOME:-$HOME/.codex}/history.jsonl\" 2>/dev/null | sort -n | tail -1 #" + (++root.nonce))
    }

    // The core's 5-minute floor keeps this from hammering the APIs.
    Timer {
        id: activityDelay
        interval: 5000
        onTriggered: root.fetch(-1)
    }

    Component.onCompleted: fetch(-1)
}
