import QtQuick
import Quickshell
import Quickshell.Io
import "Model.js" as Model

// Service owns everything that talks to the network or the disk. It keeps
// running whether or not the panel is open, because the bar count has to stay
// fresh and the heartbeat is what keeps this machine counted at all.
Item {
  id: root

  readonly property string apiBase: Quickshell.env("PULSE_API") || "https://pulse.emrecan.dev"

  property var settings: Model.defaults()
  property var catalog: ({ countries: [] })
  property var counts: ({ world: 0, country: 0, subdivision: 0 })
  property string servedSubdivision: ""
  property bool connected: false
  property int failures: 0

  property var history: null
  property double historyFetchedAt: 0

  readonly property bool configured: settings.country !== ""
  readonly property bool sharing: settings.enabled && !settings.paused && configured
  readonly property string state: !settings.enabled || !configured ? "setup"
    : settings.paused ? "paused"
    : connected ? "live" : "offline"

  // The server dropped the subdivision this client asked for, which means the
  // catalog shipped with this plugin is older than the server's.
  readonly property bool staleSubdivision: sharing && settings.subdivision !== ""
    && connected && servedSubdivision === ""

  function save(patch) {
    var next = Model.parseSettings(JSON.stringify(Object.assign({}, settings, patch)))
    settings = next
    settingsFile.setText(Model.serializeSettings(next))
    if (sharing) beatNow()
    else {
      // Nothing to tell the server: the presence simply expires (SPEC §13.3).
      connected = false
      counts = { world: 0, country: 0, subdivision: 0 }
    }
  }

  function beatNow() {
    if (!sharing || beatProc.running) return
    beatProc.command = [
      "curl", "-fsS", "--max-time", "8",
      "-X", "POST",
      "-H", "content-type: application/json",
      "-d", Model.heartbeatBody(settings.country, settings.subdivision),
      root.apiBase + "/v1/heartbeat"
    ]
    beatProc.running = true
  }

  function refreshHistory(scope, code) {
    if (!code || historyProc.running) return
    // The panel is the only thing that wants history, and it is worth ~5
    // minutes of staleness to not ask again on every open (SPEC §14.2).
    var age = Date.now() - historyFetchedAt
    if (history && history.scope === scope && history.code === code && age < 300000) return

    historyProc.command = [
      "curl", "-fsS", "--max-time", "8",
      root.apiBase + "/v1/history?scope=" + encodeURIComponent(scope) + "&code=" + encodeURIComponent(code)
    ]
    historyProc.running = true
  }

  function onBeat(raw) {
    var parsed = null
    try {
      parsed = JSON.parse(String(raw || "").trim())
    } catch (e) {
      onBeatFailed()
      return
    }
    if (!parsed || typeof parsed.world !== "number") {
      onBeatFailed()
      return
    }
    counts = {
      world: parsed.world,
      country: parsed.country,
      subdivision: parsed.subdivision
    }
    servedSubdivision = String(parsed.subdivision_code || "")
    connected = true
    failures = 0
    beatTimer.interval = Model.nextInterval(parsed.next, 0) * 1000
  }

  // A failed heartbeat is never surfaced as an error: the bar dims and the
  // next tick tries again (SPEC §41).
  function onBeatFailed() {
    connected = false
    failures = failures + 1
    beatTimer.interval = Model.nextInterval(60, failures) * 1000
  }

  Process {
    id: beatProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.onBeat(text)
    }
    onExited: function (code) {
      if (code !== 0) root.onBeatFailed()
    }
  }

  Process {
    id: historyProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try {
          var parsed = JSON.parse(String(text || "").trim())
          if (parsed && parsed.points) {
            root.history = parsed
            root.historyFetchedAt = Date.now()
          }
        } catch (e) {
          // Keep whatever was shown before; history is decoration.
        }
      }
    }
  }

  Timer {
    id: beatTimer
    interval: 60000
    repeat: true
    running: root.sharing
    triggeredOnStart: true
    onTriggered: root.beatNow()
  }

  FileView {
    id: settingsFile
    path: Quickshell.env("HOME") + "/.local/state/omarchy/settings/pulse.json"
    watchChanges: true
    atomicWrites: true
    printErrors: false
    onFileChanged: reload()
    onLoaded: root.settings = Model.parseSettings(text())
    onLoadFailed: root.settings = Model.defaults()
  }

  FileView {
    id: catalogFile
    path: Qt.resolvedUrl("locations.json").toString().replace("file://", "")
    printErrors: false
    onLoaded: {
      try {
        root.catalog = JSON.parse(text())
      } catch (e) {
        root.catalog = { countries: [] }
      }
    }
  }

  // The first settings read can race shell startup, which would leave a
  // configured user looking un-configured until their next write. One delayed
  // reload self-corrects and is a no-op when the first read was fine.
  Timer {
    interval: 1500
    running: true
    onTriggered: settingsFile.reload()
  }
}
