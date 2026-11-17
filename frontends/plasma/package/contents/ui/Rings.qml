// Desktop view: provider headings with their rings, sized to fill the applet.
import QtQuick
import QtQuick.Layouts
import QtQuick.Controls as QQC2

import org.kde.kirigami as Kirigami

MouseArea {
    id: view

    property var store
    property string layoutMode: "grid"
    property bool showHeadings: true
    property bool busy: false

    signal toggled()

    Layout.minimumWidth: Kirigami.Units.gridUnit * 4
    Layout.minimumHeight: Kirigami.Units.gridUnit * 3
    Layout.preferredWidth: Kirigami.Units.gridUnit * 24
    Layout.preferredHeight: Kirigami.Units.gridUnit * 16

    acceptedButtons: Qt.LeftButton
    onClicked: view.toggled()

    readonly property var groups: store ? store.groups : []
    readonly property var counts: {
        var c = []
        for (var i = 0; i < groups.length; i++)
            c.push(store.ringsFor(groups[i]).length)
        return c
    }
    readonly property real ringSize: store ? store.fitRingSize(counts, width, height, layoutMode, showHeadings) : 0
    readonly property bool isDark: Kirigami.Theme.backgroundColor.hslLightness < 0.5
    readonly property color trackColor: isDark ? "#33373B" : "#E5E7E8"

    Flow {
        id: flow
        anchors.fill: parent
        spacing: view.store ? view.store.groupGap(view.ringSize) : 0

        Repeater {
            model: view.groups

            Column {
                id: groupCol
                required property var modelData
                readonly property var rings: view.store.ringsFor(modelData)
                readonly property bool groupDimmed: view.store.dimmed(modelData)
                // Column mode: one group per line, so take the whole width.
                width: view.layoutMode === "column" ? flow.width : implicitWidth
                spacing: 0

                Item {
                    visible: view.showHeadings
                    width: headingRow.implicitWidth
                    height: view.store.headingHeight(view.ringSize, view.showHeadings)

                    Row {
                        id: headingRow
                        anchors.verticalCenter: parent.verticalCenter
                        spacing: Kirigami.Units.smallSpacing
                        Text {
                            text: groupCol.modelData.title
                            textFormat: Text.PlainText
                            color: view.store.groupColor(groupCol.modelData)
                            font.pixelSize: Math.max(12, view.ringSize * 0.16)
                            font.weight: Font.DemiBold
                            opacity: groupCol.groupDimmed ? 0.6 : 1
                        }
                        Kirigami.Icon {
                            visible: groupCol.modelData.status !== "ok"
                            source: groupCol.groupDimmed ? "data-warning" : "data-information"
                            width: Math.max(12, view.ringSize * 0.14)
                            height: width
                            anchors.verticalCenter: parent.verticalCenter
                        }
                    }
                    HoverHandler { id: headHover }
                    QQC2.ToolTip.visible: headHover.hovered && view.store.statusText(groupCol.modelData) !== ""
                    QQC2.ToolTip.text: view.store.statusText(groupCol.modelData)
                }

                Row {
                    spacing: view.store.ringGap(view.ringSize)
                    Repeater {
                        model: groupCol.rings
                        Ring {
                            required property var modelData
                            width: view.ringSize
                            height: view.ringSize
                            percent: modelData.percent || 0
                            arcColor: view.store.ringColor(groupCol.modelData, modelData)
                            trackColor: view.trackColor
                            textColor: Kirigami.Theme.textColor
                            subTextColor: Kirigami.Theme.disabledTextColor
                            value: view.store.valueText(modelData)
                            name: view.store.nameText(modelData)
                            countdown: view.store.countdownText(modelData)
                            dimmed: groupCol.groupDimmed
                            fontFamily: Kirigami.Theme.defaultFont.family

                            HoverHandler { id: ringHover }
                            QQC2.ToolTip.visible: ringHover.hovered
                            QQC2.ToolTip.delay: Kirigami.Units.toolTipDelay
                            QQC2.ToolTip.text: view.store.tooltip(groupCol.modelData, modelData)
                        }
                    }
                }
            }
        }
    }

    // Nothing at all yet: first run, or bingofuel missing from PATH.
    QQC2.Label {
        anchors.centerIn: parent
        width: parent.width * 0.9
        visible: view.groups.length === 0
        horizontalAlignment: Text.AlignHCenter
        wrapMode: Text.Wrap
        textFormat: Text.PlainText
        text: view.store && view.store.error !== "" ? view.store.error
              : (view.busy ? "Reading usage…" : "No providers enabled. Run: bingofuel setup")
    }
}
