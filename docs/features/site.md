# Public website

The static Astro site under `web/` (built to `web/dist`, served from `https://douglasjarquin.github.io/sum/` by `.github/workflows/deploy.yml`) presents the project: home, remainder overview, and the nine docs-shell pages.
Its copy is a snapshot ported from the design source; keep it truthful by hand when repo docs change.

| ID | Scenario | Driver | Evidence |
| --- | --- | --- | --- |
| `site.install` | Web dependencies resolve from the committed `aube-lock.yaml` | automated: `aube -C web install` inside `mise run verify` (`mise-tasks/verify`) | aggregate run record |
| `site.check` | `astro check` is clean over layouts, components, and pages | automated: `aube -C web run check` inside `mise run verify` | aggregate run record |
| `site.unit` | The `sitePath` base-join helper behaves under `node --test` | automated: `aube -C web run test:unit` inside `mise run verify` | aggregate run record |
| `site.build` | `astro build` emits all eleven routes plus the 404 under the `/sum/` base in `web/dist` | automated: `aube -C web run build` inside `mise run verify` | aggregate run record |
| `site.e2e` | Playwright drives preview: every route renders, internal links carry the `/sum/` prefix, theme override persists, the docs sidebar collapses to chips under 720px, and favicon/OG assets resolve | manual: `mise run web:deps` once, then `mise run web:test` (`web/tests/e2e/`) | playwright output; CI also runs it in `deploy.yml` |
| `site.visual` | Pages match the design in both themes at desktop and mobile widths | manual: `mise run web:dev` plus a browser pass against `web/DESIGN.md` | reviewer confirmation |
| `site.deploy` | Merging to `main` deploys `web/dist` to GitHub Pages through the `github-pages` environment | manual: `.github/workflows/deploy.yml` on a non-PR event; requires the repo's Pages source set to GitHub Actions | Pages deployment URL |
