// One gauge: grey track, arc from 6 o'clock clockwise, up to three lines inside.
import QtQuick
import QtQuick.Shapes

Item {
    id: ring

    property real percent: 0
    property color arcColor: "white"
    property color trackColor: "#33373B"
    property color textColor: "white"
    property color subTextColor: "gray"
    property string value: ""
    property string name: ""
    property string countdown: ""
    property bool dimmed: false
    property bool alert: false   // colour the name line like the arc
    property string fontFamily: ""

    readonly property real size: Math.min(width, height)
    readonly property real thickness: Math.max(2, size * 0.028)
    readonly property real radius: (size - thickness) / 2
    readonly property real innerDiameter: Math.max(0, size - thickness * 4)
    // Lines appear only when the ring is big enough to hold them.
    readonly property bool roomForName: size >= 56
    readonly property bool roomForCountdown: size >= 80

    opacity: dimmed ? 0.4 : 1

    Shape {
        anchors.fill: parent
        preferredRendererType: Shape.CurveRenderer

        ShapePath {
            strokeColor: ring.trackColor
            strokeWidth: ring.thickness
            fillColor: "transparent"
            PathAngleArc {
                centerX: ring.width / 2
                centerY: ring.height / 2
                radiusX: ring.radius
                radiusY: ring.radius
                startAngle: 0
                sweepAngle: 360
            }
        }

        ShapePath {
            strokeColor: ring.percent > 0 ? ring.arcColor : "transparent"
            strokeWidth: ring.thickness
            fillColor: "transparent"
            capStyle: ShapePath.RoundCap
            PathAngleArc {
                centerX: ring.width / 2
                centerY: ring.height / 2
                radiusX: ring.radius
                radiusY: ring.radius
                startAngle: 90
                sweepAngle: 360 * Math.max(0, Math.min(100, ring.percent)) / 100
            }
        }
    }

    Column {
        anchors.centerIn: parent
        spacing: 0

        Text {
            anchors.horizontalCenter: parent.horizontalCenter
            text: ring.value
            textFormat: Text.PlainText
            color: ring.textColor
            font.family: ring.fontFamily
            font.pixelSize: Math.max(7, ring.size * (ring.value.length > 5 ? 0.17 : 0.2))
            width: ring.innerDiameter
            horizontalAlignment: Text.AlignHCenter
            elide: Text.ElideRight
        }
        Text {
            anchors.horizontalCenter: parent.horizontalCenter
            visible: text !== "" && ring.roomForName
            text: ring.name
            textFormat: Text.PlainText
            color: ring.alert ? ring.arcColor : ring.subTextColor
            font.family: ring.fontFamily
            font.pixelSize: Math.max(6, ring.size * 0.13)
            width: ring.innerDiameter
            horizontalAlignment: Text.AlignHCenter
            elide: Text.ElideRight
        }
        Text {
            anchors.horizontalCenter: parent.horizontalCenter
            visible: text !== "" && ring.roomForCountdown
            text: ring.countdown
            textFormat: Text.PlainText
            color: ring.subTextColor
            opacity: 0.8
            font.family: ring.fontFamily
            font.pixelSize: Math.max(6, ring.size * 0.11)
            width: ring.innerDiameter
            horizontalAlignment: Text.AlignHCenter
            elide: Text.ElideRight
        }
    }
}
