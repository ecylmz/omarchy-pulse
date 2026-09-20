#!/usr/bin/env python3
"""Generate locations.json from the iso-codes package (ISO 3166-1 / 3166-2).

Arch: pacman -S iso-codes   Debian/Ubuntu: apt install iso-codes

First-level subdivisions only: ISO 3166-2 entries carrying a "parent" are
nested below another subdivision, which Pulse deliberately does not model.
"""

import json
import pathlib
import sys

SRC = pathlib.Path("/usr/share/iso-codes/json")
OUT = pathlib.Path(__file__).resolve().parent.parent


def main() -> int:
    if not SRC.is_dir():
        sys.exit(f"{SRC} not found — install the iso-codes package")

    countries = json.loads((SRC / "iso_3166-1.json").read_text())["3166-1"]
    subs = json.loads((SRC / "iso_3166-2.json").read_text())["3166-2"]

    by_country: dict[str, list[dict[str, str]]] = {}
    for s in subs:
        if "parent" in s:
            continue
        by_country.setdefault(s["code"].split("-", 1)[0], []).append(
            {"code": s["code"], "name": s["name"]}
        )

    catalog = {
        "source": "iso-codes (ISO 3166-1, ISO 3166-2)",
        "countries": [
            {
                "code": c["alpha_2"],
                "name": c["name"],
                "subdivisions": sorted(
                    by_country.get(c["alpha_2"], []), key=lambda s: s["name"]
                ),
            }
            for c in sorted(countries, key=lambda c: c["name"])
        ],
    }

    blob = json.dumps(catalog, ensure_ascii=False, separators=(",", ":")) + "\n"
    for target in (OUT / "server" / "locations.json", OUT / "plugin" / "locations.json"):
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(blob, encoding="utf-8")

    n_sub = sum(len(c["subdivisions"]) for c in catalog["countries"])
    print(f"{len(catalog['countries'])} countries, {n_sub} subdivisions, {len(blob)} bytes")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
