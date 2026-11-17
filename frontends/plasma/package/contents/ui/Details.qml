// Popup: every meter as a bar with exact reset times, details, status and actions.
import QtQuick
import QtQuick.Layouts
import QtQuick.Controls as QQC2

import org.kde.kirigami as Kirigami

Item {
    id: details

    property var store
    property real backgroundAlpha: 0.55
    property bool busy: false

    signal refreshRequested()
    signal jevBalanceEntered(real usd)

    Layout.minimumWidth: Kirigami.Units.gridUnit * 20
    Layout.preferredWidth: Kirigami.Units.gridUnit * 22
    Layout.preferredHeight: Math.min(content.implicitHeight + Kirigami.Units.largeSpacing * 2, Kirigami.Units.gridUnit * 34)

    Rectangle {
        anchors.fill: parent
        radius: Kirigami.Units.cornerRadius
        color: Kirigami.Theme.backgroundColor
        opacity: details.backgroundAlpha
    }

    QQC2.ScrollView {
        anchors.fill: parent
        anchors.margins: Kirigami.Units.largeSpacing
        contentWidth: availableWidth

        ColumnLayout {
            id: content
            width: parent.width
            spacing: Kirigami.Units.largeSpacing

            Repeater {
                model: details.store ? details.store.groups : []

                ColumnLayout {
                    id: g
                    required property var modelData
                    Layout.fillWidth: true
                    spacing: Kirigami.Units.smallSpacing

                    RowLayout {
                        Layout.fillWidth: true
                        QQC2.Label {
                            text: g.modelData.title
                            textFormat: Text.PlainText
                            color: details.store.groupColor(g.modelData)
                            font.weight: Font.DemiBold
                            font.pixelSize: Kirigami.Theme.defaultFont.pixelSize * 1.15
                        }
                        QQC2.Label {
                            visible: !!g.modelData.plan
                            text: g.modelData.plan || ""
                            textFormat: Text.PlainText
                            color: Kirigami.Theme.disabledTextColor
                        }
                        Item { Layout.fillWidth: true }
                    }

                    QQC2.Label {
                        Layout.fillWidth: true
                        visible: text !== ""
                        text: details.store.statusText(g.modelData)
                        textFormat: Text.PlainText
                        wrapMode: Text.Wrap
                        color: details.store.dimmed(g.modelData) ? Kirigami.Theme.negativeTextColor : Kirigami.Theme.neutralTextColor
                    }

                    Repeater {
                        model: g.modelData.meters || []
                        ColumnLayout {
                            id: row
                            required property var modelData
                            Layout.fillWidth: true
                            spacing: 2
                            opacity: details.store.dimmed(g.modelData) ? 0.5 : 1

                            RowLayout {
                                Layout.fillWidth: true
                                QQC2.Label {
                                    text: row.modelData.label
                                    textFormat: Text.PlainText
                                }
                                Item { Layout.fillWidth: true }
                                QQC2.Label {
                                    textFormat: Text.PlainText
                                    text: row.modelData.kind === "credit"
                                        ? details.store.valueText(row.modelData) + " left of " + details.store.formatMoney(row.modelData.total)
                                        : Math.round(row.modelData.percent) + "%"
                                }
                            }
                            Rectangle {
                                Layout.fillWidth: true
                                height: 4
                                radius: 2
                                color: Kirigami.Theme.disabledTextColor
                                opacity: 0.35
                                Rectangle {
                                    width: parent.width * Math.min(100, Math.max(0, row.modelData.percent)) / 100
                                    height: parent.height
                                    radius: 2
                                    color: details.store.ringColor(g.modelData, row.modelData)
                                }
                            }
                            QQC2.Label {
                                visible: text !== ""
                                textFormat: Text.PlainText
                                font: Kirigami.Theme.smallFont
                                color: Kirigami.Theme.disabledTextColor
                                text: {
                                    var m = row.modelData
                                    if (m.kind === "credit")
                                        return Number(m.percent).toFixed(1) + "% used" + (m.estimate ? " · estimate" : "")
                                    if (!m.resets_at) return ""
                                    return "Resets " + Qt.formatDateTime(new Date(m.resets_at), "ddd d MMM hh:mm")
                                        + " (in " + details.store.formatCountdown(m.resets_at) + ")"
                                }
                            }
                        }
                    }

                    Repeater {
                        model: g.modelData.details || []
                        QQC2.Label {
                            required property var modelData
                            Layout.fillWidth: true
                            textFormat: Text.PlainText
                            wrapMode: Text.Wrap
                            font: Kirigami.Theme.smallFont
                            color: Kirigami.Theme.disabledTextColor
                            text: modelData.label + ": " + modelData.value
                        }
                    }

                    // TypeSafe has no balance API: the balance is typed here after a top-up.
                    RowLayout {
                        visible: g.modelData.id === "typesafe"
                        Layout.fillWidth: true
                        QQC2.TextField {
                            id: balanceField
                            Layout.fillWidth: true
                            placeholderText: "New balance in USD, e.g. 4.98"
                            inputMethodHints: Qt.ImhFormattedNumbersOnly
                            validator: DoubleValidator { bottom: 0; decimals: 4; notation: DoubleValidator.StandardNotation }
                            onAccepted: setBalance.clicked()
                        }
                        QQC2.Button {
                            id: setBalance
                            text: "Update JEV balance"
                            enabled: balanceField.acceptableInput
                            onClicked: {
                                details.jevBalanceEntered(Number(balanceField.text.replace(",", ".")))
                                balanceField.text = ""
                            }
                        }
                    }

                    Kirigami.Separator { Layout.fillWidth: true; opacity: 0.4 }
                }
            }

            RowLayout {
                Layout.fillWidth: true
                QQC2.Label {
                    Layout.fillWidth: true
                    textFormat: Text.PlainText
                    font: Kirigami.Theme.smallFont
                    color: Kirigami.Theme.disabledTextColor
                    wrapMode: Text.Wrap
                    text: {
                        if (!details.store) return ""
                        var s = details.store.fetchedAtMs > 0 ? "Updated " + Qt.formatDateTime(new Date(details.store.fetchedAtMs), "hh:mm:ss") : ""
                        if (details.store.error) s += (s ? " · " : "") + details.store.error
                        if (!details.store.configFound) s += (s ? " · " : "") + "no config yet: run bingofuel setup"
                        return s
                    }
                }
                QQC2.Button {
                    icon.name: "view-refresh"
                    text: "Refresh"
                    enabled: !details.busy
                    onClicked: details.refreshRequested()
                }
            }
        }
    }
}
