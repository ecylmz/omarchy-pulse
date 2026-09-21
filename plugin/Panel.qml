pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import qs.Commons
import qs.Ui
import "Model.js" as Model

Panel {
  id: root
  moduleName: "ecylmz.omarchy-pulse"
  // Registered so the panel can be opened from a keybinding or a script:
  //   omarchy-shell ecylmz.omarchy-pulse toggle
  ipcTarget: "ecylmz.omarchy-pulse"

  property var anchorItem: null
  property var hostWidget: null
  property bool editingLocation: false
  property string draftCountry: ""
  property string draftSubdivision: ""

  // Read by BarWidget, which owns no state of its own.
  readonly property var pulseSettings: svc.settings
  readonly property var pulseCounts: svc.counts
  readonly property string pulseState: svc.state

  readonly property color foreground: Color.popups.text
  readonly property color accent: Color.accent
  readonly property string fontFamily: Style.font.family

  readonly property string view: !svc.settings.enabled ? "optin"
    : (editingLocation || !svc.configured) ? "location"
    : "main"

  readonly property bool hasHistory: svc.history && svc.history.points
    && svc.history.points.length > 0

  readonly property string subdivisionLabel: svc.settings.subdivision
    ? Model.subdivisionName(svc.catalog, svc.settings.country, svc.settings.subdivision)
    : ""

  function open() {
    if (svc.configured) svc.refreshHistory(shownScope, historyCode(shownScope))
    root.controller.show()
  }

  function close() {
    editingLocation = false
    root.controller.hide()
  }

  readonly property string shownScope: Model.effectiveScope(svc.settings)

  function historyCode(scope) {
    switch (scope) {
    case "world": return "WORLD"
    case "country": return svc.settings.country
    default: return svc.settings.subdivision
    }
  }

  function beginEditingLocation() {
    draftCountry = svc.settings.country
    draftSubdivision = svc.settings.subdivision
    editingLocation = true
  }

  function commitLocation() {
    if (!draftCountry) return
    svc.save({ country: draftCountry, subdivision: draftSubdivision, paused: false })
    editingLocation = false
    svc.refreshHistory(shownScope, historyCode(shownScope))
  }

  Service { id: svc }

  Connections {
    target: svc
    function onSettingsChanged() {
      if (root.opened && svc.configured)
        svc.refreshHistory(root.shownScope, root.historyCode(root.shownScope))
    }
  }

  KeyboardPanel {
    id: panel
    anchorItem: root.anchorItem
    owner: root.hostWidget || root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(360))
    contentHeight: panel.fittedContentHeight(content.implicitHeight, Style.space(560))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onCloseRequested: {
        if (root.editingLocation && svc.configured) root.editingLocation = false
        else root.close()
      }
      onTabRequested: function (direction) { root.switchPanel(direction) }

      Flickable {
        anchors.fill: parent
        contentWidth: width
        contentHeight: content.implicitHeight
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        flickableDirection: Flickable.VerticalFlick
        interactive: contentHeight > height
        ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

        ColumnLayout {
          id: content
          width: parent.width
          spacing: Style.spacing.lg

          // ---------------------------------------------------- opt-in view

          ColumnLayout {
            visible: root.view === "optin"
            Layout.fillWidth: true
            spacing: Style.spacing.lg

            PulseHeading { text: "Enable Pulse presence?" }

            PulseBody {
              text: "Pulse periodically tells the server the country and area you " +
                    "picked yourself, and nothing else.\n\n" +
                    "The server derives a temporary three-minute counting slot from " +
                    "your connection, so one machine cannot be counted a thousand " +
                    "times. Your IP address is never stored, never logged, and never " +
                    "used to guess where you are.\n\n" +
                    "Small areas show small numbers. If your area shows 1, that " +
                    "number is you — anyone who learns that will also see when you " +
                    "are online. Choose “Country only” if you would rather not."
            }

            RowLayout {
              Layout.fillWidth: true
              spacing: Style.spacing.controlGap

              Button {
                text: "Enable Pulse"
                bordered: true
                foreground: root.foreground
                fontFamily: root.fontFamily
                onClicked: {
                  root.beginEditingLocation()
                  svc.save({ enabled: true, paused: false })
                }
              }

              Button {
                text: "Not now"
                foreground: root.foreground
                fontFamily: root.fontFamily
                onClicked: root.close()
              }
            }
          }

          // -------------------------------------------------- location view

          ColumnLayout {
            visible: root.view === "location"
            Layout.fillWidth: true
            spacing: Style.spacing.lg

            PulseHeading { text: "Choose where you are" }

            SearchableDropdown {
              Layout.fillWidth: true
              label: "Country"
              placeholderText: "Search countries…"
              options: Model.countryOptions(svc.catalog)
              value: root.draftCountry
              fontFamily: root.fontFamily
              onChanged: function (v) {
                root.draftCountry = v
                root.draftSubdivision = ""
              }
            }

            SearchableDropdown {
              Layout.fillWidth: true
              enabled: root.draftCountry !== ""
              label: "Area"
              placeholderText: "Search areas…"
              options: Model.subdivisionOptions(svc.catalog, root.draftCountry)
              value: root.draftSubdivision
              fontFamily: root.fontFamily
              onChanged: function (v) { root.draftSubdivision = v }
            }

            PulseCaption {
              text: "Country only is a complete answer. Pulse never asks for a " +
                    "street, a neighbourhood, or a coordinate."
            }

            RowLayout {
              Layout.fillWidth: true
              spacing: Style.spacing.controlGap

              Button {
                text: svc.configured ? "Save" : "Start sharing presence"
                bordered: true
                enabled: root.draftCountry !== ""
                foreground: root.foreground
                fontFamily: root.fontFamily
                onClicked: root.commitLocation()
              }

              Button {
                visible: svc.configured
                text: "Cancel"
                foreground: root.foreground
                fontFamily: root.fontFamily
                onClicked: root.editingLocation = false
              }
            }
          }

          // ------------------------------------------------------ main view

          ColumnLayout {
            visible: root.view === "main"
            Layout.fillWidth: true
            spacing: Style.spacing.lg

            RowLayout {
              Layout.fillWidth: true
              PulseHeading { text: "PULSE" }
              Item { Layout.fillWidth: true }
              Text {
                text: svc.state === "live" ? "● live"
                  : svc.state === "paused" ? "◌ paused" : "◌ offline"
                color: svc.state === "live" ? root.accent : root.foreground
                opacity: svc.state === "live" ? 1.0 : 0.55
                font.family: root.fontFamily
                font.pixelSize: Style.font.caption
              }
            }

            ColumnLayout {
              Layout.fillWidth: true
              Layout.topMargin: Style.spacing.sm
              spacing: Style.spacing.md

              PulseCountRow {
                visible: root.subdivisionLabel !== ""
                label: root.subdivisionLabel
                value: svc.counts.subdivision
                emphasised: root.shownScope === "subdivision"
              }
              PulseCountRow {
                label: Model.countryName(svc.catalog, svc.settings.country)
                value: svc.counts.country
                emphasised: root.shownScope === "country"
              }
              PulseCountRow {
                label: "World"
                value: svc.counts.world
                emphasised: root.shownScope === "world"
              }
            }

            PulseBody {
              Layout.topMargin: Style.spacing.xs
              text: Model.presenceLabel(Model.scopeCount(svc.counts, root.shownScope))
            }

            PulseCaption {
              visible: svc.staleSubdivision
              text: "The server does not know your area any more. You are being " +
                    "counted for your country. Pick your area again to fix it."
            }

            PanelSeparator { Layout.fillWidth: true }

            PulseCaption { text: "LAST 24 HOURS" }

            Text {
              Layout.fillWidth: true
              visible: root.hasHistory
              text: Model.sparkline(root.hasHistory ? svc.history.points : [],
                                    28, 86400, Math.floor(Date.now() / 1000))
              color: root.foreground
              opacity: 0.85
              font.family: root.fontFamily
              font.pixelSize: Style.font.subtitle
            }

            PulseCaption {
              visible: !root.hasHistory
              text: "No history yet. The server records an aggregate count every " +
                    "five minutes."
            }

            ColumnLayout {
              Layout.fillWidth: true
              visible: root.hasHistory
              spacing: Style.spacing.sm
              PulseCountRow {
                label: "Peak today"
                value: svc.history ? svc.history.today_peak : 0
              }
              PulseCountRow {
                label: "Quietest"
                value: svc.history ? svc.history.today_low : 0
              }
              PulseCountRow {
                label: "Peak this week"
                value: svc.history ? svc.history.week_peak : 0
              }
            }

            PanelSeparator { Layout.fillWidth: true }

            RowLayout {
              Layout.fillWidth: true
              spacing: Style.spacing.controlGap
              PulseCaption { text: "Sharing as" }
              Item { Layout.fillWidth: true }
              Text {
                text: Model.locationLabel(svc.catalog, svc.settings.country, svc.settings.subdivision)
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
              }
              Button {
                text: "Change"
                foreground: root.foreground
                fontFamily: root.fontFamily
                fontSize: Style.font.caption
                onClicked: root.beginEditingLocation()
              }
            }

            RowLayout {
              Layout.fillWidth: true
              spacing: Style.spacing.controlGap
              PulseCaption { text: "Show in bar" }
              Item { Layout.fillWidth: true }
              Repeater {
                model: ["world", "country", "subdivision"]
                delegate: Button {
                  required property string modelData
                  text: modelData === "subdivision" ? "area" : modelData
                  selected: root.shownScope === modelData
                  enabled: modelData !== "subdivision" || svc.settings.subdivision !== ""
                  foreground: root.foreground
                  fontFamily: root.fontFamily
                  fontSize: Style.font.caption
                  onClicked: {
                    svc.save({ bar_scope: modelData })
                    svc.refreshHistory(root.shownScope, root.historyCode(root.shownScope))
                  }
                }
              }
            }

            RowLayout {
              Layout.fillWidth: true
              spacing: Style.spacing.controlGap
              PulseCaption { text: "Status" }
              Item { Layout.fillWidth: true }
              Text {
                text: svc.settings.paused ? "○ Paused" : "● Active"
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
              }
              ToggleSwitch {
                checked: !svc.settings.paused
                foreground: root.foreground
                onToggled: svc.save({ paused: !svc.settings.paused })
              }
            }

            PulseCaption {
              visible: svc.settings.paused
              text: "Nothing is being sent. Your presence disappears on its own " +
                    "within three minutes."
            }

            PanelSeparator { Layout.fillWidth: true }

            Text {
              text: "#OmarchyPulse"
              color: root.accent
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
            }
          }
        }
      }
    }
  }

  component PulseHeading: Text {
    color: root.foreground
    font.family: root.fontFamily
    font.pixelSize: Style.font.title
  }

  component PulseBody: Text {
    Layout.fillWidth: true
    color: root.foreground
    opacity: 0.8
    wrapMode: Text.WordWrap
    font.family: root.fontFamily
    font.pixelSize: Style.font.bodySmall
  }

  component PulseCaption: Text {
    Layout.fillWidth: true
    color: root.foreground
    opacity: 0.55
    wrapMode: Text.WordWrap
    font.family: root.fontFamily
    font.pixelSize: Style.font.caption
  }

  component PulseCountRow: RowLayout {
    id: countRow
    property string label: ""
    property int value: 0
    property bool emphasised: false

    Layout.fillWidth: true
    spacing: Style.spacing.controlGap

    Text {
      text: countRow.label
      color: root.foreground
      opacity: countRow.emphasised ? 1.0 : 0.7
      font.family: root.fontFamily
      font.pixelSize: Style.font.bodySmall
    }
    Item { Layout.fillWidth: true }
    Text {
      text: Model.formatCount(countRow.value)
      color: countRow.emphasised ? root.accent : root.foreground
      font.family: root.fontFamily
      font.pixelSize: countRow.emphasised ? Style.font.subtitle : Style.font.bodySmall
    }
  }
}
