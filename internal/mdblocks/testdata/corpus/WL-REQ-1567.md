
A timestamp shown to a person SHOULD be displayed in that person's time zone. The cockpit uses the browser's time zone. The CLI uses the local time zone and formats the value with the user's locale. `--json` output is machine-readable, so it keeps the stored UTC value (WL-REQ-1289).

