# Omarchy Pulse

Ambient presence for Omarchy. It answers one question — **how many of us are
here right now?** — for your area, your country, and the world, and nothing
else.

Pulse is not a social network. There is no chat, no profile, no username, no
user list. It does not connect you to people; it reminds you that they are
there. Anyone who wants to actually find the other person uses the
**#OmarchyPulse** hashtag, outside Pulse.

## Install

```sh
omarchy plugin add https://github.com/ecylmz/omarchy-pulse.git --enable
```

Presence sharing stays **off** until you enable it. Click the `◎` in the bar,
read what it shares, and decide.

## Usage

- Left-click the indicator to open the panel.
- Pick a country, and optionally an area. Country only is a complete answer.
- Choose whether the bar counts the world, your country, or your area.
- Toggle **Status** to pause. Your presence disappears on its own within three
  minutes; nothing is sent while paused.

The bar shows `◉ 16` when live, `◎` before setup, and `◌` when there is no
number to show — hover it for the reason.

## What it shares

Every 60 seconds, one request containing the country and area **you picked by
hand**, and nothing else:

```json
{ "country": "TR", "subdivision": "TR-55" }
```

No account, no email, no username, no device identifier, no GPS, and no
IP-based location guessing. There is nothing in the payload derived from your
machine, your user, or your network.

The server does use your connection, in one narrow way: it hashes your address
in memory with a secret that exists only while it is running, to derive a
three-minute counting slot, so that one machine cannot be counted a thousand
times. That address is never stored, never written to a log, and never attached
to any history. Presence expires by itself; recorded history is aggregate counts
only.

One consequence is worth knowing before you share an area: **a small number is
exact.** If your area shows `1`, that number is you. Anyone who works out who
it is can also see when you are online. Choose country only if you would rather
not.

## Requirements

- Omarchy Quattro
- `curl` from the base system

The catalog of countries and areas is generated from ISO 3166 and ships with
the plugin, so the picker works offline.

## Tests

```sh
tests/run.sh
```

## Remove

```sh
omarchy plugin remove ecylmz.omarchy-pulse
```

Your settings stay at `~/.local/state/omarchy/settings/pulse.json`; delete it
if you want them gone. Nothing needs to be told on the server — your presence
expires within three minutes of the last heartbeat.

## Server

The backend is a separate project:
[omarchy-pulse-server](https://github.com/ecylmz/omarchy-pulse-server). Its
`SPEC.md` is the design and threat model for the whole system, including how
the count is kept from being forged.

## License

[MIT](LICENSE) © 2026 Emre Can Yılmaz
