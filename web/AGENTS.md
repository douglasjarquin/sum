# sum website

Static Astro site deployed to GitHub Pages at `https://douglasjarquin.github.io/sum/` by `.github/workflows/deploy.yml`.
The design contract is `DESIGN.md`; page copy is a snapshot of the design source, synced by hand.

## Conventions

- Static output with `base: "/sum"`; every internal URL goes through `sitePath()` in `src/lib/site-path.mjs` so preview, Playwright, and Pages agree on the trailing-slash contract.
- `SiteLayout.astro` owns the document shell: head meta, fonts, topbar, theme script, and the `foot` variant prop (`home` / `remainder` / `docs`). `DocsLayout.astro` adds the sidebar shell for the nine docs routes.
- Tokens and shared element classes live in `src/styles/global.css`; route-only styling stays in the page. Where the design deviates per instance, prefer an inline `style` attribute (the source mockup's own idiom) over a new class.
- The theme toggle is the only client JavaScript. Pages must render fully with JavaScript disabled.
- Run repo-level commands as `mise run web:*`; from `web/` use `aube run <script>` or `aube -C web ...` from the root.
- `web/.npmrc` keeps `node-linker=hoisted`: Astro prerender resolves native bindings from an npm-compatible tree, not aube's isolated layout.
- `web/test/` holds node --test unit tests; `web/tests/e2e/` holds Playwright specs run against `astro preview` on `PLAYWRIGHT_PORT` (default 4321). Run `mise run web:deps` once per machine for the Chromium browser.
- `node_modules/`, `.astro/`, `dist/`, `test-results/`, and `playwright-report/` are generated output, never source.
- The canonical verifier (`mise run verify`) installs web deps, runs `check`, `test:unit`, and `build`; the e2e suite also runs in the deploy workflow.
