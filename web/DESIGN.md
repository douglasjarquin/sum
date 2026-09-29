# sum site design

This file is the design contract for `web/`.
It distills the source bundle (`Sum Site.dc.html`, `Sum Design System.dc.html`, `sum.css`, `Sum Brand.dc.html`); the bundle itself is not committed.
When the mockup and this file disagree on page content, the mockup was authoritative at port time; where they diverge intentionally it is noted below.

## World

A kraft-paper terminal: warm light surface, near-black rules, thin borders, no radii, no shadows.
Dark mode is a dimmed inversion of the same world, not a separate design.
Body copy is monospace; only display headings, pull quotes, and flow labels are serif.

## Tokens

| Token | Light | Dark |
| --- | --- | --- |
| `--bg` | `#f4f1ea` | `#131415` |
| `--ink` | `#17181a` | `#ece9e1` |
| `--body` | `#3a3935` | `#bdbab2` |
| `--muted` | `#5e5c56` | `#8f8d86` |
| `--rule` | `#17181a` | `rgba(236,233,225,.32)` |
| `--accent` | `oklch(0.52 0.17 250)` | `oklch(0.72 0.17 250)` |
| `--soft` | `rgba(23,24,26,.05)` | `rgba(236,233,225,.05)` |
| `--blend` | `multiply` | `screen` |
| `--mono` | `'JetBrains Mono',ui-monospace,monospace` | same |
| `--serif` | `'Instrument Serif',serif` | same |

Light `--rule` equals `--ink`: rules are thin black lines.
Dark `--rule` is translucent so hairlines stay thin at low luminance.

## Theme mechanism

Tokens live on `:root`.
`html[data-theme="dark"]` switches to the dark set; `@media (prefers-color-scheme: dark) { :root:not([data-theme]) }` applies the same set when the OS is dark and no override exists.
The mockup writes `data-theme` on `<body>`; this implementation writes it on `<html>` (`document.documentElement`) so the tokens on `:root` and the `:root:not(...)` guard stay coherent — same semantics, one root.

- The toggle is the only JavaScript on the site.
- It always writes an explicit override: the opposite of the *effective* theme.
- The override persists in `localStorage` under `sum-site-theme` and is applied by an inline head script before first paint.
- With no override, the label reads the theme it will switch to (`light` under a dark OS) and the title is `Following system theme`; with an override the title is `Theme override on; following X`.
- The toggle is hidden unless JS ran (`html.js`), since it is dead weight otherwise.
- The OS media query alone restyles the page with zero JS.

## Typography

- Body: JetBrains Mono 12.5px / 1.6, color `--body`; `--ink` for literal terms (`code`, strong).
- Labels/eyebrows: 10.5px uppercase, letter-spacing .08em, `--muted`.
- h1 (doc pages): Instrument Serif 400, 44px / 1.
- `.display` (home hero): clamp(52px,7vw,92px) / .95.
- `em` inside a display h1: serif italic in `--accent`.
- h2 (doc sections): serif 26px / 1.1.
- Cell titles: serif 24px (feature cells), 22px (docs index), 19px (gate grid); kv flow labels serif 18px.
- `p.lede`: 13px; blockquote: serif 17px with a 2px `--accent` left border.
- Footer/topbar chrome: 11px uppercase .08em tracking (topbar), 11px `--muted` (foot).

Font URL is exactly `css2?family=JetBrains+Mono:wght@400;500;600&family=Instrument+Serif:ital@0;1&display=swap` — the 600 weight carries the wordmark and `strong`; serif italic carries hero `em` and quotes.

## Geometry

- `.page`: max-width 1240px, centered, 1px `--rule` left/right edges.
- `.band` / `.cells` / `.grid3`: flex or grid children separated by `gap:1px` over a `--rule` background — rules are gap seams, never borders on the cells themselves.
- `.band` spans the page width with a bottom rule; `.cells` is a bordered inset grid for use inside prose; `.grid3` is the bordered 3-up grid (gate cells).
- Doc sections: `.sec` = `padding:28px 0; border-top:1px solid var(--rule)`.
- Docs shell `.shell`: sidebar `flex:1 1 220px`, content `flex:999 1 480px`, prose column capped at `.doc` 760px.
- Code blocks are bordered divs (`.codeblock`) of `white-space:pre` lines — no `pre`+button chrome, no copy control.

## Elements

- `.topbar`: mark + `sum` wordmark (600) + muted tagline, right cluster of nav links including `github ↗` (all `--body`), theme toggle (1px `--rule` chip).
- `.side`: rail nav; entries are 7px/24px padded links with a transparent 2px left border; active entry is accent + 600 + accent left border and `aria-current="page"`.
  Group labels (`sum`, `source`) are eyebrow-style rows.
  Below 720px the rail collapses to wrapped chips (pure CSS media query — the mockup did this with a resize listener; same look, no JS).
- `.kv`: grid label/value rows separated by hairlines; labels accent by default (`.kv.flow` uses serif 18px ink labels for the you/coordinator/worker flows).
  Column width is set per instance (110-160px, or `auto 1fr` for link rows); `.kv.md`/`.kv.lg` adjust row padding (12px/16px vs 10px).
- `.cells` cards never nest; `.feature` cells carry the `NN` accent number and serif title.
- `.chip`: 1px `--rule` border, 11px.
- `.btn`: solid `--ink` fill; `.btn.outline` transparent with `--rule` border, `--soft` hover.
- `.foot`: per-screen footer strip (two variants: home, docs).

## The mark

Six overlapping discs, `oklch(0.72 0.17 H)` at 85% opacity, `mix-blend-mode: var(--blend)` (`multiply` light, `screen` dark) — the only theme-dependent part.
In a 22px box the 12.5px discs sit at left/top: (7.9,4.8) h25, (6.4,7.5) h80, (3.2,7.5) h145, (1.5,4.8) h200, (3.1,2) h250, (6.4,2) h300.
`Mark.astro` renders them at that fixed 22px size — the only use is the topbar.
`Sum Brand.dc.html` specifies a different dark recipe (oklch 0.68 / .6 opacity); the site mockup's single recipe is what the site uses.

## Content model

Nine designed routes plus a styled 404: `/`, `/docs/`, `/install/`, `/architecture/`, `/verification/`, `/skills/`, `/configuration/`, `/herdr/`, `/update/`.
Page copy is ported verbatim from the mockup; the one deliberate divergence is canonical skill names (`sum-work`/`sum-deliver`/`sum-status`) where the mockup used the old aliases (`sum-worker`/`sum-delivery`/`sum-rundown` — still present in `skills/` as aliases).
This copy is a snapshot: when the in-repo docs change, the site is updated by hand; there is no generated sync.

## Accessibility and browser surfaces

- Real landmarks (`header`, `nav`, `main`, `footer`), real links, `aria-current` on the active nav entry.
- Focus uses `:focus-visible` with a 1px accent outline; text selection uses `--accent`/`--bg`.
- Full content renders with JavaScript disabled; the theme toggle is the only script-gated element.
- Contrast: `--body`/`--muted` on `--bg` clear 4.5:1 for body text in both themes (verify when changing tokens, not per page).
