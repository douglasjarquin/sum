# sum website

Static Astro site deployed to GitHub Pages at `https://douglasjarquin.github.io/sum/` by `.github/workflows/deploy.yml`.
The design contract is `DESIGN.md`; page copy is a snapshot of the design source, synced by hand.
The repository-level guidance in `../AGENTS.md` still applies here.

## Conventions

- Static output with `base: "/sum"`; every internal route URL goes through `sitePath()` in `src/lib/site-path.mjs` so preview, Playwright, and Pages agree on the trailing-slash contract. Files in `public/` are not routes — join `import.meta.env.BASE_URL` directly with no trailing slash (the `asset()` helper in `SiteLayout.astro`).
- `SiteLayout.astro` owns the document shell: head meta, fonts, topbar, theme script, and the `foot` variant prop (`home` / `docs`). `DocsLayout.astro` adds the sidebar shell for the eight docs routes.
- Tokens and shared element classes live in `src/styles/global.css`; route-only styling stays in the page. Where the design deviates per instance, prefer an inline `style` attribute (the source mockup's own idiom) over a new class.
- The theme toggle is the only client JavaScript. Pages must render fully with JavaScript disabled.
- Run repo-level commands as `mise run web:*`; from `web/` use `aube run <script>` or `aube -C web ...` from the root.
- `mise run web:dev` pins `ASTRO_DEV_BACKGROUND=1`: without it `astro dev` detects an agentic environment and daemonizes, exiting the task while a detached server keeps running.
- `web/.npmrc` keeps `node-linker=hoisted`: Astro prerender resolves native bindings from an npm-compatible tree, not aube's isolated layout.
- `web/test/` holds node --test unit tests; `web/tests/e2e/` holds Playwright specs run against `astro preview` on `PLAYWRIGHT_PORT` (default 4321). Run `mise run web:deps` once per machine for the Chromium browser.
- `node_modules/`, `.astro/`, `dist/`, `test-results/`, and `playwright-report/` are generated output, never source.
- The canonical verifier (`mise run verify`) installs web deps from the frozen lockfile, runs `check`, `test:unit`, `build`, and asserts every route was emitted to `dist/`; the e2e suite also runs in the deploy workflow.
