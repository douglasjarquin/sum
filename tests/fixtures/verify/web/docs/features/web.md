# Service

| ID | Scenario | Driver | Evidence |
| --- | --- | --- | --- |
| `web.build` | `mise run build` renders `src/` into `dist/` | automated: check.py | verify.log |
| `web.serve-home` | `GET /` on the running service returns the rendered page | automated: check.py | verify.log |
| `web.visual-layout` | The page looks right in a browser at 375px and 1280px | manual: open the URL in a browser | screenshot |
