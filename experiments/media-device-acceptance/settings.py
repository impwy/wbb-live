#!/usr/bin/env python3
"""Strict, non-executing settings reader shared by preflight and start.

The bcrypt value MUST be single-quoted in .env. Compose treats single-quoted
values literally, and this reader removes the same quotes before validation.
"""
import ipaddress
import pathlib
import re
import shlex
import sys
from urllib.parse import urlsplit

KEYS = {
    "WBB_MEDIA_AUTH_USER", "WBB_MEDIA_AUTH_HASH", "WBB_MEDIA_ORIGIN",
    "WBB_MEDIA_HOSTNAME", "WBB_MEDIA_HTTPS_PORT", "WBB_MEDIA_BIND_IP",
    "WBB_MEDIA_ICE_HOST", "WBB_MEDIA_ICE_PORT", "WBB_MEDIA_ENCODER",
}


def valid_host(value):
    """Accept canonical IPv4 or lower-case DNS names, including localhost."""
    try:
        address = ipaddress.ip_address(value)
    except ValueError:
        if re.fullmatch(r"[0-9.]+", value) or not 1 <= len(value) <= 253:
            return False
        labels = value.split(".")
        return all(re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", part)
                   for part in labels)
    return address.version == 4 and str(address) == value


def load():
    path = pathlib.Path(__file__).with_name(".env")
    if not path.is_file():
        raise ValueError(".env missing")
    values = {}
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        key, sep, raw = line.partition("=")
        if not sep or key not in KEYS or key in values:
            raise ValueError("unknown, duplicate or malformed setting")
        if key == "WBB_MEDIA_AUTH_HASH":
            if len(raw) < 2 or raw[0] != "'" or raw[-1] != "'":
                raise ValueError("WBB_MEDIA_AUTH_HASH must be single-quoted")
            value = raw[1:-1]
        else:
            if not raw or any(c.isspace() or c in "\"'`$\\" for c in raw):
                raise ValueError(f"invalid {key}")
            value = raw
        values[key] = value
    if set(values) != KEYS:
        raise ValueError("required settings missing")
    if not re.fullmatch(r"[A-Za-z0-9_-]{1,40}", values["WBB_MEDIA_AUTH_USER"]):
        raise ValueError("invalid WBB_MEDIA_AUTH_USER")
    if not re.fullmatch(r"\$2[aby]\$\d{2}\$[./A-Za-z0-9]{53}", values["WBB_MEDIA_AUTH_HASH"]):
        raise ValueError("invalid WBB_MEDIA_AUTH_HASH")
    host = values["WBB_MEDIA_HOSTNAME"]
    ice = values["WBB_MEDIA_ICE_HOST"]
    for label, name in (("WBB_MEDIA_HOSTNAME", host), ("WBB_MEDIA_ICE_HOST", ice)):
        if not valid_host(name):
            raise ValueError(f"invalid {label}")
    try:
        bind_ip = ipaddress.ip_address(values["WBB_MEDIA_BIND_IP"])
    except ValueError as exc:
        raise ValueError("invalid WBB_MEDIA_BIND_IP") from exc
    if bind_ip.is_unspecified or bind_ip.version != 4:
        raise ValueError("WBB_MEDIA_BIND_IP must name one IPv4 interface")
    for key in ("WBB_MEDIA_HTTPS_PORT", "WBB_MEDIA_ICE_PORT"):
        raw = values[key]
        if not re.fullmatch(r"[0-9]{1,5}", raw) or not 1 <= int(raw) <= 65535 or str(int(raw)) != raw:
            raise ValueError(f"invalid {key}")
    if values["WBB_MEDIA_HTTPS_PORT"] == values["WBB_MEDIA_ICE_PORT"]:
        raise ValueError("HTTPS and ICE ports must differ")
    origin = values["WBB_MEDIA_ORIGIN"]
    parsed = urlsplit(origin)
    port = values["WBB_MEDIA_HTTPS_PORT"]
    expected = f"https://{host}" + ("" if port == "443" else f":{port}")
    if origin != expected or parsed.username or parsed.password or parsed.path or parsed.query or parsed.fragment:
        raise ValueError("WBB_MEDIA_ORIGIN must exactly match HTTPS hostname and port")
    if values["WBB_MEDIA_ENCODER"] not in ("cpu", "vaapi"):
        raise ValueError("WBB_MEDIA_ENCODER must be cpu or vaapi")
    return values


def main():
    try:
        values = load()
    except ValueError as exc:
        print(f"settings: {exc}", file=sys.stderr)
        return 1
    if len(sys.argv) != 2 or sys.argv[1] not in ("check", "shell", "render"):
        print("usage: settings.py check|shell|render", file=sys.stderr)
        return 2
    if sys.argv[1] == "shell":
        for key in sorted(values):
            print(f"{key}={shlex.quote(values[key])}")
    elif sys.argv[1] == "render":
        base = pathlib.Path(__file__).parent
        template = (base / "mediamtx.yml").read_text()
        if template.count("__ICE_HOST__") != 1 or template.count("__ICE_PORT__") != 1:
            raise ValueError("MediaMTX template markers missing")
        rendered = template.replace("__ICE_HOST__", values["WBB_MEDIA_ICE_HOST"])
        rendered = rendered.replace("__ICE_PORT__", values["WBB_MEDIA_ICE_PORT"])
        run = base / "run"
        run.mkdir(exist_ok=True)
        (run / "mediamtx.yml").write_text(rendered)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
