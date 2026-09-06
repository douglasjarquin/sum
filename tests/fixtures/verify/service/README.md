# Items service

A tiny JSON HTTP service used as a verification fixture.

- `mise run serve` starts it on 127.0.0.1 with an ephemeral port and prints the URL.
- `GET /health` answers `{"ok": true}` when the process is ready.
- `GET /items` lists stored items; `POST /items` with `{"name": "..."}` stores one.
- Items persist in `ITEMS_DATA_DIR` (default `./data`); point it at a temporary directory for tests.
- `mise run test` runs the unit suite.
