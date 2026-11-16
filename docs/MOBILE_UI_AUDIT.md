# Scalper — Mobile UI Audit

**Target:** https://130.210.18.97.sslip.io/ (`Scalper v0.29.0`, serve mode, live feed)
**Date:** 2026-11-16
**Scope:** Layout/visual/ergonomics audit of the phone experience. No code changes made.

---

## 1. How this was measured

Chrome DevTools MCP is **not configured** for this workspace (no `mcp` block in `opencode.json`, and no MCP servers are attached to the session), so the audit was driven directly against Google Chrome over the DevTools Protocol (CDP) with device emulation — the same engine, viewport override, `deviceScaleFactor`, touch flag and mobile UA that the DevTools MCP server uses. Every number below is read from the live page (`getBoundingClientRect`, `getComputedStyle`, canvas backing-store sizes), not estimated from screenshots.

| Device profile | CSS viewport | DPR | Notes |
|---|---|---|---|
| **Galaxy S26 Ultra** (primary) | **384 × 832** | 3.75 | `SM-S948B`, Chrome 141 Android UA |
| Galaxy S22 | 360 × 780 | 3.0 | worst-case narrow device |
| Pixel 8 | 412 × 915 | 2.6 | sanity check |
| S26 Ultra landscape | 832 × 384 | 2.0 | orientation check |

Session was authenticated with `akshay` / device-binding session token so the terminal itself (not the lock screen) was measured.

---

## 2. Summary

> **Status after the follow-up fix pass (2026-11-16/17):** every finding below is closed and verified at 320–1600 px plus landscape, with one follow-up review pass on the chart engine and one on the desktop breakpoint.
>
> - **Fixed:** M1, M2, M4, M6, M8–M16, M12's raw-error dump, M3 (compact ladder ≤ ~28 % of the plot), M5 (canvases backed at `css × min(dpr,3)`; exports 3× and crisp), M7 (axis labels/tags clamped inside the canvas), M17/M18 (axis badges stack with 2 px gaps, in-plot read-out suppressed on narrow panes), M19 (`title`/`aria-label` on clipped labels), M20 (drawer content-height, 52 px rows), and the desktop 1025–1366 horizontal overflow (header/toolbar wrap, no control hidden, `BUY/SELL/ASK` inside every pad).
> - **Accepted residuals:** at 320×568 the candle pane is 100 px (the volume pane collapses to keep the price pane usable); at 832×384 it is 89 px (the landscape chart is short but functional); the retina bitmap is capped at 3× so a 3.75 dppx panel is slightly downsampled; the AI evaluator's VWAP comes from the broker's `avg_cost` because the feed exposes no per-trade size.

| ID | Area | Severity | Issue |
|----|------|----------|-------|
| M1 | Header | **P0** | Header overflows the viewport by 12 px: the index/LTP block collapses to **0 px** and is painted **underneath** the P&L chip, `MARGIN` is hidden entirely, and the ⚙ menu button is half cut off |
| M2 | Bottom nav | **P0** | 5 view tabs need 431 px in a 384 px bar → **POSITIONS is cut off** (`right: 420.9`) |
| M3 | Charts | **P0** | Order-flow/DOM depth panel is 150 px wide inside a 322 px candle canvas (**≈47 %**) and is drawn **on top of the candles** — the right half of the price action is hidden |
| M4 | First-run | **P0** | "Install Scalper PWA" banner is pinned above the bottom nav and **covers the Buy/Sell/Ask row** |
| M5 | Charts | **P1** | Chart canvases render with a **1× backing store** (322×375 bitmap for a 322×375 CSS box at DPR 3) → soft/blurry candles, indicators and axis text on every real phone |
| M6 | Layout | **P1** | 56 px of dead black space between the terminal content and the bottom nav (double-counted safe area) |
| M7 | Charts | **P1** | Right-most x-axis time label is clipped (`15:0…`) — the canvas runs flush to the screen edge |
| M8 | Touch | **P1** | 20 interactive controls below the 44 × 44 minimum (down to **20 × 12**) |
| M9 | Type | **P1** | Axis/labels render at **8–9 px** (`UNDERLYING`, `EXPIRY`, `P&L`, `REQ MARGIN`, `LOTS`, `DAY`, SL warning) |
| M10 | Toolbar | **P1** | Toolbar is a 707 px scroll strip in a 384 px viewport (179 % overflow); the EXPIRY select is visibly cut at 360 px, with no scroll affordance |
| M11 | Positions/Orders | **P1** | 5-tab strip overflows; last tab ("Trades") is cut in half. "SQUARE OFF ALL" occupies a full row of its own leaving a large void |
| M12 | AI Signals modal | **P1** | Raw HTTP-429 JSON (≈ 6 lines of escaped API text) is printed in the UI as the error message |
| M13 | Option chain modal | **P1** | Search placeholder truncated to `Search strike (e.`; "Load more below" floats **over** the last row |
| M14 | Order pad | **P1** | Primary trade buttons are 122 × 44 but labelled only `B` / `S`, while the secondary button is fully spelled `Ask` |
| M15 | Empty states | **P1** | Positions/Orders pane is 620 px tall with a single "No open positions." line at ~⅓ height |
| M16 | Landscape | **P1** | At 832 × 384 the page falls back to the **desktop** layout and lays out at **1395 px** wide → header, ATM chip, CE/SPOT/PE toggles and the footer are all cut |
| M17 | Charts | **P2** | Last-price tag, VWAP tag and Max-Pain tag overlap each other and the depth ladder |
| M18 | Charts | **P2** | VWAP read-out is drawn in the top-left of the plot where it collides with the session-high marker |
| M19 | Panels | **P2** | Subtitles truncate mid-word with no tooltip: `Total …`, `SL 13.56% > 1…`, `Strike PCR 0.4…` |
| M20 | Drawer | **P2** | Drawer is fine functionally, but each row is ~74 px tall and the sheet uses only the vertical middle of the screen |

---

## 3. Detailed findings

### M1 — Header is crushed and half the metrics are unreachable (P0)

**Observed:** the 44 px header holds a brand block, an index picker, a P&L chip, a feed dot, an AI Signals button and a gear. At 384 px it overflows, and two blocks are literally painted on top of each other:

```
.brand               x=10.0   w=89.8
.spot-picker         x=107.8  w=0.0                 ← index picker container has ZERO width
#spot-current        x=107.8  w=16.0  h=28          ← …but its children still render:
  #spot-live-dot       x=115.8 w=7
  #spot-current-price  x=134.8 w=59.6  → ends 194.4  (NIFTY 25,291.10)
  .spot-current-caret  x=202.4 w=4.4
.topbar-right        x=115.8  w=280.5 right=396.2   ← starts at the same x=115.8
  .metric (P&L)        x=115.8 w=32.5 h=26.4        ← overlaps the spot price
    .metric-label        x=121.8 w=20.5 (8.5 px)
    #pnl-chip            x=129.6 w=12.7
  #feed-badge          x=153.3 w=20.0 h=12.0        ← 20 × 12 tap target
  #ai-signals-button   x=178.3 w=180.9              ← 47 % of the header width
  #mobile-menu-btn     x=364.2 w=32.0 right=396.2   ← gear is 12.2 px off-screen
```

So the live NIFTY price and the `P&L ₹0` read-out occupy the same pixels (x 115.8–194 vs 115.8–148.3): in `01-header-collision.png` the price is hidden underneath `P&L` (the small rounded shape there is the 16 px-wide `#spot-current` button). The only route into settings/theme/pairing is a 32 px gear that is clipped by the right edge. `MARGIN` is separately hidden by `.metric:nth-child(1) { display: none }` (`app.css:2576`), so it is simply absent on every phone.

**Cause:** `internal/web/static/app.css:2651` (`.topbar-right`) is `flex-wrap: nowrap` with no `min-width: 0` discipline, and `:2718` gives `.spot-picker` `flex: 1 1 auto; min-width: 0` — so it absorbs all the shrink and collapses to zero while its children keep their intrinsic size and overflow. Nothing shrinks or hides `#ai-signals-button` (181 px) at phone widths.

**Fix direction:** at ≤ 480 px, hide the AI Signals label (keep the 🤖 + % pill), move P&L/MARGIN into the drawer, give `.topbar-right` `min-width: 0` + `flex-shrink: 0` on the gear, and let `.spot-picker` keep a minimum (e.g. `min-width: 132px`) because the index + LTP is the most important header datum. Verify the header at 320 / 360 / 384 / 412.

---

### M2 — Bottom tab bar overflows; POSITIONS is cut (P0)

Measured children of `#mobile-nav` (bar is 384 px wide, `overflow-x: auto`):

| Tab | x | width | right |
|---|---|---|---|
| CALL | 10 | 68.9 | 78.9 |
| SPOT | 84.9 | 70.4 | 155.3 |
| PUT | 161.3 | 61.8 | 223.1 |
| CHAIN | 229.1 | 77.9 | 307.0 |
| **POSITIONS** | 313.0 | 107.9 | **420.9** |

`POSITIONS` — the tab that shows open risk — is the one that falls off the screen (`05-positions-tabs-clipped.png`). It is technically scrollable, but there is no gradient/peek affordance, so it reads as a rendering bug.

**Cause:** `app.css:3042` — `.mobile-nav-btn { flex: 1 1 0 }` without `min-width: 0`, so the flex items refuse to shrink below their content width and the row overflows.

**Fix direction:** `grid-template-columns: repeat(5, minmax(0, 1fr))` (or `min-width: 0` + `padding: 0 4px`), drop the "0" badge to a superscript dot, and consider shortening `POSITIONS` → `POS` on ≤ 360 px.

---

### M3 — The depth / order-flow panel covers the candlesticks (P0)

`03-spot-depth-overlay-covers-candles.png` is the default mobile spot view. The candle canvas is 322 px wide; the order-flow/DOM panel is drawn from x = 168 to x = 318 (**150 px, 47 %** of the plot), so the right half of the price action is hidden behind the bubbles, and the `MP: 25,200` Max-Pain tag and the last-price tag are drawn *through* the ladder.

**Cause:** `web/src/charts.ts:866`:

```ts
const panelWidth = Math.min(240, Math.max(150, Math.round(width * 0.28)));
const xRight = width - 4;
```

`Math.max(150, …)` is a **desktop floor**: at `width = 322` the 28 % rule would give 90 px, but the 150 px floor wins. `showDepth` defaults to `true` (`charts.ts:2838`) and is persisted, so this is the default experience. `computeDOMInterval()` (`charts.ts:531`) already takes the real pane height, so the row density adapts — the width does not.

**Fix direction:** reserve a plot gutter instead of overlaying (`chart.setStyles` / `layout` padding), or make the panel width `min(150, width * 0.35)` and auto-disable depth below ~480 px, or collapse it to a per-row 2-number strip on the right edge with a toggle. Do not let the panel exceed ~30 % of the canvas on phones.

---

### M4 — Install banner hides the trade buttons on first visit (P0)

`02-install-banner-over-orderpad.png`: the PWA install banner is `position: fixed; bottom: calc(56px + safe-area); left/right: 10px` (`app.css:3905`) and sits directly on top of the `B / S / Ask` row and the qty stepper. A first-time phone user cannot see the order pad's primary actions until they dismiss a marketing banner.

**Fix direction:** auto-dismiss after ~8 s, only appear after the first interaction (or after N visits), render it below the order pad / as a slim 40 px chip, and never overlay `#btn-ask-*` / `.trade.buy` / `.trade.sell`.

---

### M5 — Chart canvases are rendered at 1× on a 3× screen (P1)

Measured inside the CALL pane:

| canvas | CSS size | bitmap size | ratio |
|---|---|---|---|
| candle pane | 322 × 249 | 322 × 249 | **1.0** |
| volume pane | 322 × 100 | 322 × 100 | **1.0** |
| price axis | 48 × 249 | 48 × 249 | **1.0** |

`window.devicePixelRatio === 3` on this profile (3.75 on a real S26 Ultra). Every chart glyph, 9 px indicator label and 1 px candle wick is therefore drawn into a 1× buffer and upscaled by the compositor — visibly soft next to the crisp DOM text (`11-canvas-1x-backing-store.png`).

**Fix direction:** size the backing store to `cssSize × min(devicePixelRatio, 3)` and let CSS scale it down (klinecharts v10 creates the canvases itself, so this needs either the library's DPR hook or a wrapper that re-assigns `canvas.width/height` after `chart.resize()`), then re-`resize()` on `devicePixelRatio` change / orientation change.

This also caps the sharpness of the new chart export (PNG/JPEG): the exporter composites these same 1× canvases, so exported images are upscaled just like the screen. Fixing the DPR sizing fixes both.

---

### M6 — 56 px dead band above the bottom nav (P1)

`#mobile-nav` is a flex sibling (`order: 99`) of `.terminal`, yet `.terminal` also carries `padding-bottom: calc(56px + env(safe-area-inset-bottom))` (`app.css:3108`). Measured `_gapAfterLastPane = 56` on every view — a black stripe between the order pad / positions panel and the nav bar. On a 832 px tall screen that is 6.7 % of the working area lost.

**Fix:** remove the extra padding when the nav is out of flow, or let `.terminal` be `min-height: 0; flex: 1 1 auto` and let the nav take its own space.

---

### M7 — Right-most x-axis label is clipped (P1)

`xaxis-right.png` shows the last time label rendered as `15:0…` — the canvas ends exactly at the viewport edge, so the final glyph is cut. The price axis also has **no right gutter** at all (`150.00` touches x = 384).

**Fix:** add 6–8 px of right padding inside the chart (or a `padding-right` on `.chart-canvas`), and clamp the last x-axis label so it right-aligns inside the plot.

---

### M8 — Touch targets below 44 × 44 (P1)

Measured interactive elements at 384 px (width × height in CSS px):

| Control | Size |
|---|---|
| `#feed-badge` (status) | **20 × 12** |
| `#spot-current` (index picker) | 16 × 28 |
| `#theme-toggle` (logo) | 28 × 28 |
| `#mobile-menu-btn` (⚙) | 32 × 32 |
| `.install-banner-close` | 32 × 32 |
| `#index-select` / `#expiry-select` | 145 × 34 / 108 × 34 |
| timeframe chips `1m 3m 5m 15m 1h 4h` | 34 × 34 each |
| `.pro-tools-toggle` ("Tools") | 64 × 24 |
| `.unit-toggle` (LOTS / DAY) | 38 × 38 |
| qty `−` / `+` | 44 × 38 |

Apple/Material guidance is 44 × 44 (Android 48 × 48). The timeframe chips and the LOTS/DAY toggles are the most-used of these.

---

### M9 — 8–9 px type (P1)

`TINY FONTS` measured on the default screen: `.metric-label` 8.5 px (`P&L`), `span` 8 px (`UNDERLYING`, `EXPIRY`), `.leg-margin-label` 9 px (`REQ MARGIN`), `.leg-sl-risk` 9 px (`⚠ SL 13.56% > 1.00%`), `.unit-toggle` 9 px (`LOTS`, `DAY`). Below ~11 px CSS text becomes hard to read on a 6.9″ phone held at arm's length, and it fails WCAG text sizing expectations for a data-critical trading UI.

---

### M10 — Toolbar is a 179 %-wide scroll strip with no affordance (P1)

`.toolbar` scroll content is **707 px** inside a **384 px** viewport. `#tf-group` starts at x = 388.5 (i.e. entirely off-screen initially) and the `ATM 25300` chip reaches x = 694. At 360 px the EXPIRY select itself is cut (`09-360px-clipping.png`). The scrollbar is styled 4 px tall and effectively invisible on touch.

**Fix:** two rows (row 1: index + expiry; row 2: timeframe + strike + toggles), or a horizontally-scrollable chip row with an edge fade + snap, plus `scroll-snap-type` and a "more ▸" chevron.

---

### M11 — Positions/Orders tab strip clipped, and a wasted row (P1)

`05-positions-tabs-clipped.png`: `Brackets · Positions · Open · Orders · Tr…` — the 5th tab is cut. Each tab is `min-height: 44px; padding: 8px 14px; white-space: nowrap; flex-shrink: 0` (`app.css:3546`), so the strip is ~500 px wide by construction. Above it, `SQUARE OFF ALL` sits alone on its own row and leaves a ~300 px empty void to its left.

**Fix:** shorten labels (`Open`→`Live`, `Brackets`→`SL/TP`), or move to a 2-row segmented control, and pair the destructive `SQUARE OFF ALL` with a summary (P&L / open lots) so the row is not empty.

---

### M12 — AI Signals modal prints a raw API error (P1)

`07-ai-modal-raw-error.png`: the error block is the literal OpenRouter 429 body —

> `{"error":{"message":"Rate limit exceeded: free-models-per-day…","headers":{"X-RateLimit-Limit":"50","X-RateLimit-Remaining":"0"…` (truncated)

…rendered as six lines of red text. It overflows the card, wraps at 9–11 px, and tells the user nothing actionable. The `Next Scan:` label also wraps to two lines next to `04:48`.

**Fix:** map upstream errors to a short human message ("Daily free-model quota exhausted — retry in 04:48 or add credits"), keep the raw payload behind a collapsible "details", and give the `Next scan` cell a single-line layout.

---

### M13 — Option-chain modal details (P1)

* Search field placeholder is clipped: `Search strike (e.` — measured `181 × 38 px` at x = 112 in a 384 px row that also holds a `CALL ▾` select and an `⚡ ATM` button, so `Search strike (e.g. 24500)…` can never fit (and 38 px is under the 44 px tap minimum).
* `Load more below` is an **absolutely floating** button that covers the last strike row (`25600` is half hidden).
* Row height is comfortable (good), but the `CALL (LTP / VOL)` / `STRIKE` / `PUT (LTP / VOL)` header sits inside a separately-styled box that does not line up with the column grid below.

**Fix:** collapse `CALL ▾` into the header, give the search field `flex: 1 1 auto; min-width: 0` with a shorter placeholder, and make "Load more" an in-flow row (or a sticky footer with a backdrop).

---

### M14 — `B` / `S` buttons (P1)

The most important buttons on the screen are 122 × 44 and contain a single letter, while the least important one reads `Ask` (`index.html:213–215`). Tapping `B` vs `S` in a live market with a 1-letter label and 8 px gap is a mis-trade risk.

**Fix:** `BUY 82.00` / `SELL 81.95` (pull LTP into the label) or at minimum `BUY CE` / `SELL CE`, and colour-code with more than a subtle border tint.

---

### M15 — Empty state wastes the whole pane (P1)

Positions view: 620 px tall pane, tabs at the top, "No open positions." floating at y ≈ 1070/2556 — i.e. vertically centred in a giant empty card. Same for Orders.

**Fix:** compact empty state (icon + one line + a "Open option chain" action) at the top of the content area, and let the chart stay visible underneath.

---

### M16 — Landscape falls into the desktop layout (P1)

At 832 × 384 the `(max-width: 768px)` mobile rules stop applying, and the desktop grid needs ~1400 px. Measured `innerWidth = 1395` (the layout viewport expands), and `10-landscape-desktop-layout.png` shows: `AI Signals` cut at the right edge, `ATM` chip cut, the `CE/SPOT/PE` toggle cut, `SCALPER v0.29.0 · serve mode` footer overlapping the Positions panel, and the chart squeezed to ~200 px tall.

**Fix:** add a tablet/landscape range (`769–1024 px`, or `(max-height: 500px)`) that keeps the stacked/mobile chrome, or make the desktop grid `minmax(0, 1fr)`-based and switch to the single-column stack up to ~1100 px (the repo already has `app.css:2579` doing a partial version of this).

---

### M17–M20 — Smaller polish items

* **M17 Tag collisions:** at the right edge, the last-price tag (`80.00`), the VWAP tag (`80.10`) and the Max-Pain tag (`MP: 25,200`) are drawn on the same y and overlap; they also sit on top of the depth ladder.
* **M18 VWAP label:** `VWAP: 80.10` is drawn at the plot's top-left, where it collides with the session-high marker (`168.85` in `04-ce-chart-clipped-axis.png`). Move indicator read-outs to a single stacked legend line under the pane title.
* **M19 Truncation:** pane subtitles are cut with `…` and no `title`/tap-to-expand: `17 NOV · 5m · Strike PCR 0.48 · Total …`, `⚠ SL 13.56% > 1…`.
* **M20 Drawer:** `08-drawer.png` — rows are 74 px tall (Pair Device, Register, Settings, Theme, Unpair, Sign Out) with ~400 px of empty space at the bottom; usable, but it is a full-height sheet for 6 actions.

---

## 4. Suggested order of work

1. **M1 + M2 + M6** — header and bottom-nav geometry. These are pure CSS, fix the two most visible "broken" impressions, and cost the least.
2. **M3 + M5 + M7** — chart rendering: stop the depth panel from covering the candles on phones, fix the DPR backing store, add the right gutter. This is the single biggest perceived-quality jump.
3. **M4 + M12 + M13** — stop overlays/raw errors from blocking the trade path.
4. **M8 + M9 + M10 + M14** — touch sizes, minimum type scale, toolbar restructure, `BUY`/`SELL` labels.
5. **M11 + M15 + M16** — positions/orders tabs, empty states, landscape/tablet breakpoint.

A useful guardrail while doing this: the app has a single `@media (max-width: 768px)` block (`app.css:2596`) covering everything from 320 px to 768 px. Most of the failures above are because that one breakpoint is asked to serve a 6.9″ phone and a 10″ tablet at once. Splitting into `≤ 430` (phone), `431–768` (large phone/small tablet), `769–1024` (tablet/landscape) and adding a 320 px smoke test would prevent regressions.

---

## 5. Reproducing

The measurement harness used for this audit is a throwaway Puppeteer-core script (Chrome + CDP, `setViewport({ isMobile: true, hasTouch: true, deviceScaleFactor })`, session token injected into `localStorage.scalper_serve_session`). It is not committed. If you want it in-repo as a regression check, say so and it can be added under `scripts/` as `mobile_audit.mjs` with a `--device` flag.
