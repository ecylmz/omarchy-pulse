import QtQuick
import Quickshell
import qs.Ui
import "Model.js" as Model

BarWidget {
  id: root
  moduleName: "ecylmz.omarchy-pulse"

  readonly property bool opened: panelLoader.item ? panelLoader.item.opened === true : false
  readonly property bool popoutSwitchClosing: panelLoader.item ? panelLoader.item.popoutSwitchClosing === true : false
  readonly property var pulseSettings: panelLoader.item ? panelLoader.item.pulseSettings : Model.defaults()
  readonly property var pulseCounts: panelLoader.item ? panelLoader.item.pulseCounts : null
  readonly property string pulseState: panelLoader.item ? panelLoader.item.pulseState : "setup"

  function open() { if (panelLoader.item) panelLoader.item.open() }
  function close() { if (panelLoader.item) panelLoader.item.close() }
  function toggle() { if (panelLoader.item) panelLoader.item.toggle() }
  function closeForPopoutSwitch() { if (panelLoader.item) panelLoader.item.closeForPopoutSwitch() }

  function injectPanel() {
    if (!panelLoader.item) return
    panelLoader.item.bar = root.bar
    panelLoader.item.anchorItem = button
    panelLoader.item.hostWidget = root
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onBarChanged: injectPanel()

  Loader {
    id: panelLoader
    active: true
    source: Qt.resolvedUrl("Panel.qml")
    visible: false
    onLoaded: {
      root.injectPanel()
      Qt.callLater(root.injectPanel)
    }
  }

  WidgetButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: Model.barText(root.pulseSettings.bar_style,
                        Model.scopeCount(root.pulseCounts, Model.effectiveScope(root.pulseSettings)),
                        root.pulseState)
    tooltipText: {
      switch (root.pulseState) {
      case "setup": return "Omarchy Pulse — not set up yet"
      case "paused": return "Omarchy Pulse — paused"
      case "connecting": return "Omarchy Pulse — connecting…"
      case "offline": return "Omarchy Pulse — can't reach the server"
      }
      return "Omarchy Pulse — " + Model.presenceLabel(
        Model.scopeCount(root.pulseCounts, Model.effectiveScope(root.pulseSettings)))
    }
    onPressed: function (buttonCode) {
      if (buttonCode === Qt.LeftButton) root.toggle()
    }
  }
}
