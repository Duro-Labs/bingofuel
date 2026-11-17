// A tooltip that never interprets its text as HTML: labels and messages come
// from provider data, and the stock tooltips use AutoText.
import QtQuick
import QtQuick.Controls as QQC2

QQC2.ToolTip {
    id: tip
    contentItem: QQC2.Label {
        text: tip.text
        textFormat: Text.PlainText
        wrapMode: Text.NoWrap
    }
}
