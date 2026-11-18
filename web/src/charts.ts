import * as Patterns from "./chart/patterns";
import * as Indicators from "./chart/indicators";
import { calculateFibLevels } from "./chart/fibonacci";
import { buildVolumeProfile, suggestVolumeRanges } from "./chart/volumeProfile";
import { atrSeries, atrValue, oiDeltaSeries, supertrendSeries } from "./chart/intraday";
import { istSessionKey } from "./chart/session";

// Minimum gap between volume-profile rebuilds while ticks stream in.
const VOLUME_PROFILE_REFRESH_MS = 5000;

const chartColors = (element) => {
  const styles = getComputedStyle(element);
  const color = (name, fallback) => styles.getPropertyValue(name).trim() || fallback;
  return {
    bg: color("--chart-bg", "#07090c"),
    grid: color("--chart-grid", "#141c24"),
    text: color("--chart-text", "#8292a2"),
    crosshair: color("--chart-crosshair", "#8293a3"),
    up: color("--green", "#35c46a"),
    down: color("--red", "#ff5d5d"),
    border: color("--line", "#24303a"),
    resistance: color("--chart-res", "#f05a5a"),
    support: color("--chart-sup", "#29b360"),
    accent: color("--accent", "#f0b429"),
  };
};

const toChartTimestamp = (time) => (time > 1e11 ? time : time * 1000);

const VWAP_COLOR = "#8b5cf6";

const istDayFormatter = new Intl.DateTimeFormat("en-GB", {
  timeZone: "Asia/Kolkata", year: "numeric", month: "2-digit", day: "2-digit",
});
const istDayKey = (time) => {
  const parts = istDayFormatter.formatToParts(new Date(toChartTimestamp(time)));
  const pick = (type) => parts.find((part) => part.type === type)?.value || "";
  return `${pick("year")}-${pick("month")}-${pick("day")}`;
};

const istDateTimeFormatter = new Intl.DateTimeFormat("en-GB", {
  timeZone: "Asia/Kolkata",
  year: "numeric", month: "2-digit", day: "2-digit",
  hour: "2-digit", minute: "2-digit", hourCycle: "h23",
});
const formatIstDateTime = (time) => {
  const parts = istDateTimeFormatter.formatToParts(new Date(toChartTimestamp(time)));
  const pick = (type) => parts.find((part) => part.type === type)?.value || "";
  return `${pick("year")}-${pick("month")}-${pick("day")} ${pick("hour")}:${pick("minute")}`;
};

const pad2 = (value) => String(value).padStart(2, "0");
const formatIstNow = () => {
  // IST has no DST, so a fixed +05:30 offset is exact.
  const shifted = new Date(Date.now() + 5.5 * 3600 * 1000);
  return `${shifted.getUTCFullYear()}-${pad2(shifted.getUTCMonth() + 1)}-${pad2(shifted.getUTCDate())}`
    + `T${pad2(shifted.getUTCHours())}:${pad2(shifted.getUTCMinutes())}:${pad2(shifted.getUTCSeconds())}+05:30`;
};
const fmtPrice = (value) => {
  const number = Number(value);
  return Number.isFinite(number) ? number.toFixed(2) : "-";
};
const fmtCompact = (value) => {
  const number = Number(value);
  if (!Number.isFinite(number)) return "-";
  return Number.isInteger(number) ? String(number) : number.toFixed(2);
};
const encodeCanvas = (canvas, mime, quality) =>
  new Promise((resolve, reject) => {
    if (typeof canvas.toBlob === "function") {
      canvas.toBlob((blob) => {
        if (blob) resolve(blob);
        else reject(new Error("Canvas encoding returned an empty blob"));
      }, mime, quality);
      return;
    }
    try {
      const dataUrl = canvas.toDataURL(mime, quality);
      const binary = atob(dataUrl.split(",")[1] || "");
      const bytes = new Uint8Array(binary.length);
      for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
      resolve(new Blob([bytes], { type: mime }));
    } catch (error) {
      reject(error);
    }
  });

const formatVolume = (val) => {
  if (!Number.isFinite(val) || val <= 0) return "0";
  if (val >= 1e7) return `${(val / 1e7).toFixed(2)}Cr`;
  if (val >= 1e5) return `${(val / 1e5).toFixed(2)}L`;
  if (val >= 1e3) return `${(val / 1e3).toFixed(1)}K`;
  return String(Math.round(val));
};

/* ── Chart rendering geometry ─────────────────────────────── */

// Every klinecharts canvas is backed by cssSize × pixelRatio device pixels.
// Rendering above 3x triples the bitmap for no visible gain, so it is capped.
const MAX_CANVAS_PIXEL_RATIO = 3;
// A chart container narrower than this cannot show the full DOM ladder or the
// indicator read-outs without covering the candles, so both switch to compact
// layouts. 1440x900 desktop panes measure ~463px; phone panes 370px or less.
// Compare it against the full pane width, never the plot width: the price axis
// is 48-68px depending on the digits, so a plot-width test flips two panes of
// the same screen into different layouts on a one-pixel difference.
const NARROW_PANE_WIDTH = 440;
// Blank strip kept inside the y-axis canvas so price tags never touch the edge.
const RIGHT_AXIS_GUTTER = 6;
// Blank strip kept inside the x-axis canvas so the last time label is not cut.
const X_AXIS_EDGE_GUTTER = 8;
// Minimum vertical gap between two stacked price tags on the axis.
const TAG_STACK_GAP = 2;

// The candle pane must keep a readable plot: on a 126px phone container the
// library's default stack of 100px indicator panes squeezes it to zero, so the
// indicator panes give way instead.
const CANDLE_MIN_HEIGHT = 120;
// An indicator pane shorter than this cannot show a usable plot, so it is
// hidden entirely rather than squeezed into an unreadable sliver.
const INDICATOR_PANE_MIN_HEIGHT = 44;
// When the pane stack does not fit, indicator panes may take at most this share
// of the container height.
const INDICATOR_MAX_HEIGHT_RATIO = 0.3;
// klinecharts' defaults for indicator panes, used when restoring their size.
const DEFAULT_INDICATOR_PANE_HEIGHT = 100;
const LIBRARY_PANE_MIN_HEIGHT = 30;
// Measured x-axis height (tick size + margins) until the pane has laid out.
const DEFAULT_X_AXIS_HEIGHT = 25;

// The raw ratio drives the matchMedia watcher; only the bitmap allocation is
// capped, so a 3.75dppx phone still arms a `(resolution: 3.75dppx)` listener.
const rawDevicePixelRatio = () => {
  const ratio = Number(typeof window === "undefined" ? 1 : window.devicePixelRatio);
  return Number.isFinite(ratio) && ratio > 0 ? ratio : 1;
};

const canvasPixelRatio = () => Math.min(rawDevicePixelRatio(), MAX_CANVAS_PIXEL_RATIO);

let measureContext: CanvasRenderingContext2D | null = null;
const measureTextWidth = (text, size, weight, family) => {
  const value = String(text == null ? "" : text);
  if (!value) return 0;
  if (!measureContext) {
    measureContext = document.createElement("canvas").getContext("2d");
  }
  if (!measureContext) return value.length * (Number(size) || 12) * 0.6;
  measureContext.font = `${weight || "normal"} ${Number(size) || 12}px ${family || "Helvetica Neue"}`;
  return measureContext.measureText(value).width;
};

/**
 * Keep an overlay's text pill inside the plot: labels anchored to a candle (or
 * a signal marker) near the right edge would otherwise be sliced by the canvas.
 */
const clampTextFigureX = (attrs, styles, width) => {
  const total = Number(width) || 0;
  if (!attrs || !(total > 0) || !Number.isFinite(attrs.x)) return attrs;
  const text = String(attrs.text == null ? "" : attrs.text);
  if (!text) return attrs;
  const boxWidth = (Number(styles?.paddingLeft) || 0)
    + measureTextWidth(text, styles?.size, styles?.weight, styles?.family)
    + (Number(styles?.paddingRight) || 0)
    + 2 * (Number(styles?.borderSize) || 0);
  const align = attrs.align || "left";
  if (align === "center") {
    if (attrs.x + boxWidth / 2 > total) {
      attrs.x = total - 1;
      attrs.align = "right";
    } else if (attrs.x - boxWidth / 2 < 0) {
      attrs.x = 1;
      attrs.align = "left";
    }
  } else if (align === "right") {
    if (attrs.x > total - 1) attrs.x = total - 1;
    if (attrs.x - boxWidth < 0) attrs.x = boxWidth;
  } else {
    if (attrs.x < 0) attrs.x = 0;
    if (attrs.x + boxWidth > total) attrs.x = total - boxWidth;
  }
  return attrs;
};

const paneWidth = (container) => {
  const width = Number(container?.clientWidth);
  return Number.isFinite(width) && width > 0 ? width : 0;
};

// Every canvas of a chart: pane main/overlay layers, price axes and the time axis.
const chartCanvasWidgets = (chart) => {
  const widgets = [];
  if (!chart) return widgets;
  const panes = [];
  try {
    panes.push(...(chart.getDrawPanes?.() || []));
  } catch (_) {}
  try {
    panes.push(chart.getXAxisPane?.());
  } catch (_) {}
  for (const pane of panes) {
    if (!pane) continue;
    if (pane._mainWidget) widgets.push(pane._mainWidget);
    const yWidgets = pane._yAxisWidgets;
    if (yWidgets && typeof yWidgets.values === "function") {
      for (const widget of yWidgets.values()) widgets.push(widget);
    }
  }
  const canvases = [];
  for (const widget of widgets) {
    if (widget._mainCanvas) canvases.push(widget._mainCanvas);
    if (widget._overlayCanvas) canvases.push(widget._overlayCanvas);
  }
  return canvases;
};

// True for light chart backgrounds (#fff, #f8f9fa, ...), used to pick DOM row
// colours that stay legible on both themes.
const isLightBackground = (color) => {
  const value = String(color || "").trim();
  const match = /^#?([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(value);
  if (!match) return false;
  const hex = match[1].length === 3
    ? match[1].split("").map((c) => c + c).join("")
    : match[1];
  const r = parseInt(hex.slice(0, 2), 16);
  const g = parseInt(hex.slice(2, 4), 16);
  const b = parseInt(hex.slice(4, 6), 16);
  return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255 > 0.6;
};

const domRowColors = (background = null) => (isLightBackground(background)
  ? {
      panelBg: "rgba(255, 255, 255, 0.55)",
      spine: "rgba(15, 23, 42, 0.2)",
      buyText: "#15803d",
      buyPill: "rgba(240, 253, 244, 0.92)",
      sellText: "#b91c1c",
      sellPill: "rgba(254, 242, 242, 0.92)",
    }
  : {
      panelBg: "rgba(15, 23, 42, 0.45)",
      spine: "rgba(255, 255, 255, 0.22)",
      buyText: "#86efac",
      buyPill: "rgba(6, 30, 18, 0.82)",
      sellText: "#fca5a5",
      sellPill: "rgba(38, 8, 8, 0.82)",
    });

// The DOM ladder keeps its original desktop footprint; on narrow panes it
// shrinks to a compact two-number strip that covers ~25% of the plot instead of
// the old 150px floor (47% of a 322px phone canvas). `plotWidth` is the drawing
// area; `paneWidth` (plot + price axis) decides the layout so every pane of the
// same width picks the same style regardless of how many digits its prices have.
const depthLadderLayout = (plotWidth, paneWidth = plotWidth) => {
  const total = Number(plotWidth) || 0;
  const pane = Number(paneWidth) || total;
  const compact = pane > 0 && pane < NARROW_PANE_WIDTH;
  const panelWidth = compact
    ? Math.min(112, Math.max(64, Math.round(total * 0.28)))
    : Math.min(240, Math.max(150, Math.round(total * 0.28)));
  const right = total - 4;
  return { compact, panelWidth, right, left: right - panelWidth };
};

// Full pane width of a y-axis, or the plot width when the axis internals are
// unavailable (overlay sizing must never throw).
const axisPaneWidth = (axis, plotWidth) => {
  try {
    const width = Number(axis?.getParent?.()?.getBounding?.().width);
    return Number.isFinite(width) && width > 0 ? width : plotWidth;
  } catch (_) {
    return plotWidth;
  }
};

let canvasPixelRatioPatched = false;
// A klinecharts bump that renames the internals only has to be detected once:
// after that every chart keeps the library's own scheduler.
let canvasPixelRatioDisabled = false;
/**
 * klinecharts v10 backs its canvases with `window.devicePixelRatio`, but its
 * `device-pixel-content-box` ResizeObserver path reports CSS pixels in some
 * Chromium builds (and in emulated mobile viewports), which leaves the whole
 * pane on a 1x bitmap. Take over the canvas draw scheduler so the bitmap is
 * always allocated from the final CSS box × pixelRatio at paint time: that also
 * removes the 1x-then-3x re-allocation flash when a pane is first measured.
 */
function patchCanvasPixelRatio(chart) {
  if (canvasPixelRatioPatched || canvasPixelRatioDisabled) return;
  try {
    const panes = chart?.getDrawPanes?.() || chart?._drawPanes || [];
    let canvasWidget = null;
    for (const pane of panes) {
      canvasWidget = pane?._mainWidget?._mainCanvas || null;
      if (canvasWidget) break;
    }
    const proto = canvasWidget ? Object.getPrototypeOf(canvasWidget) : null;
    if (!proto || typeof proto._scheduleDraw !== "function") return;
    // The replacement drives the library's own draw listener; only take over
    // when the expected internals are present.
    if (typeof canvasWidget._listener !== "function" || typeof canvasWidget._ctx?.scale !== "function") return;
    if (!canvasWidget._element || typeof canvasWidget._element.getContext !== "function") return;
    const originalScheduleDraw = proto._scheduleDraw;
    let verified = false;
    const restore = (reason) => {
      // A klinecharts upgrade that renames the internals we allocate around
      // must leave the library in charge instead of blanking every canvas.
      canvasPixelRatioPatched = false;
      try {
        proto._scheduleDraw = originalScheduleDraw;
      } catch (_) {}
      if (!canvasPixelRatioDisabled) {
        canvasPixelRatioDisabled = true;
        console.warn(`chart canvas pixel ratio patch disabled (${reason}); using the library scheduler`);
      }
    };
    canvasPixelRatioPatched = true;

    proto._scheduleDraw = function(pending = false) {
      const widget = this;
      if (!widget._element || typeof widget._element.getContext !== "function") {
        restore("canvas element missing");
        return;
      }
      // -1 is the library's "no frame queued" sentinel for this field.
      const idle = -1;
      if (!widget._resizePending) widget._resizePending = Boolean(pending);
      if (widget._requestAnimationId !== undefined && widget._requestAnimationId !== null && widget._requestAnimationId !== idle) return;
      const frame = typeof requestAnimationFrame === "function"
        ? requestAnimationFrame
        : (callback) => setTimeout(callback, 16);
      widget._requestAnimationId = frame(() => {
        widget._requestAnimationId = idle;
        if (widget._destroyed) return;
        const element = widget._element;
        // Let the restored (library) scheduler paint this widget, so disabling
        // the patch cannot leave the pane blank until the next data tick.
        const resumeLibrary = () => {
          try {
            originalScheduleDraw.call(widget, pending);
          } catch (_) {}
        };
        // `_ctx` belongs to the widget, so check it per widget at paint time
        // rather than once on the instance the patch was installed from.
        const ctx = widget._ctx;
        if (!element || typeof element.getContext !== "function" || !ctx || typeof ctx.setTransform !== "function") {
          restore("canvas context missing");
          return;
        }
        const width = Number(widget._width) || 0;
        const height = Number(widget._height) || 0;
        if (!Number.isFinite(Number(widget._width)) || !Number.isFinite(Number(widget._height))) {
          // Renamed fields would compute a NaN pixel size below.
          restore("internal dimensions missing");
          resumeLibrary();
          return;
        }
        // A sized-and-visible canvas whose internal CSS box reads 0 means the
        // `_width`/`_height` fields were renamed: bail out before overwriting
        // its bitmap with a zero-sized one.
        const styled = /px$/.test(String(element.style?.width || ""))
          && /px$/.test(String(element.style?.height || ""));
        const sized = Number(element.clientWidth) > 0 && Number(element.clientHeight) > 0;
        if ((!(width > 0) || !(height > 0)) && styled && sized) {
          restore("internal dimensions changed");
          resumeLibrary();
          return;
        }
        if (widget._resizePending) {
          widget._resizePending = false;
          if (width > 0 && height > 0) {
            const ratio = canvasPixelRatio();
            widget._pixelWidth = Math.max(1, Math.round(width * ratio));
            widget._pixelHeight = Math.max(1, Math.round(height * ratio));
            widget._nextPixelWidth = widget._pixelWidth;
            widget._nextPixelHeight = widget._pixelHeight;
            element.width = widget._pixelWidth;
            element.height = widget._pixelHeight;
          } else {
            // Hidden pane: the library computes a negative plot width from a
            // zero-sized container. Keep the canvas empty instead of writing an
            // invalid bitmap size, which throws and aborts the next paint.
            widget._pixelWidth = 0;
            widget._pixelHeight = 0;
            widget._nextPixelWidth = 0;
            widget._nextPixelHeight = 0;
            element.width = 0;
            element.height = 0;
          }
        } else if (width > 0 && height > 0) {
          // Self-heal a stale backing store (monitor/zoom DPR change, or a pane
          // that was re-shown at the size it had before it was hidden).
          const ratio = canvasPixelRatio();
          const expectedWidth = Math.max(1, Math.round(width * ratio));
          const expectedHeight = Math.max(1, Math.round(height * ratio));
          if (widget._pixelWidth !== expectedWidth || widget._pixelHeight !== expectedHeight) {
            widget._pixelWidth = expectedWidth;
            widget._pixelHeight = expectedHeight;
            widget._nextPixelWidth = expectedWidth;
            widget._nextPixelHeight = expectedHeight;
            element.width = expectedWidth;
            element.height = expectedHeight;
          }
        }
        // Prove on the first patched frame that the bitmap we allocated is the
        // one the library's fields describe. If a klinecharts bump renames
        // `_width`/`_height`, this mismatch restores the original scheduler.
        if (!verified && width > 0 && height > 0) {
          const ratio = canvasPixelRatio();
          const expectedWidth = Math.max(1, Math.round(width * ratio));
          const expectedHeight = Math.max(1, Math.round(height * ratio));
          if (element.width !== expectedWidth || element.height !== expectedHeight) {
            restore(`bitmap size mismatch (${element.width}x${element.height} != ${expectedWidth}x${expectedHeight})`);
            resumeLibrary();
            return;
          }
          verified = true;
        }
        if (!(widget._pixelWidth > 0) || !(widget._pixelHeight > 0)) return;
        ctx.setTransform(1, 0, 0, 1, 0, 0);
        ctx.clearRect(0, 0, widget._pixelWidth, widget._pixelHeight);
        ctx.save();
        if (width > 0 && height > 0) {
          ctx.scale(widget._pixelWidth / width, widget._pixelHeight / height);
        }
        try {
          widget._listener();
        } finally {
          ctx.restore();
        }
      });
    };
  } catch (error) {
    console.warn("chart canvas pixel ratio patch failed", error);
  }
}

let yAxisGutterPatched = false;
/**
 * The library draws the last-price / crosshair / indicator tags flush against
 * the y-axis canvas edge. Shift those label views a few pixels left so every
 * tag keeps its right padding inside the canvas.
 */
function patchYAxisRightGutter(chart) {
  if (yAxisGutterPatched) return;
  try {
    const panes = chart?.getDrawPanes?.() || chart?._drawPanes || [];
    let view = null;
    for (const pane of panes) {
      const widgets = pane?._yAxisWidgets;
      if (!widgets || typeof widgets.values !== "function") continue;
      const widget = widgets.values().next()?.value;
      view = widget?._candleLastPriceLabelView || null;
      if (view) break;
    }
    if (!view) return;
    yAxisGutterPatched = true;
    // Each tag view owns its own `drawImp`; patch the concrete classes so the
    // translate stays scoped to the y-axis label drawing.
    const seen = new Set();
    for (const name of ["_candleLastPriceLabelView", "_crosshairHorizontalLabelView"]) {
      let instance = null;
      for (const pane of panes) {
        const widgets = pane?._yAxisWidgets;
        if (!widgets || typeof widgets.values !== "function") continue;
        for (const widget of widgets.values()) {
          instance = widget?.[name] || instance;
          if (instance) break;
        }
        if (instance) break;
      }
      if (!instance) continue;
      const proto = Object.getPrototypeOf(instance);
      if (!proto || seen.has(proto) || typeof proto.drawImp !== "function") continue;
      seen.add(proto);
      const drawImp = proto.drawImp;
      proto.drawImp = function(ctx) {
        // The library anchors these labels on the inner edge of the axis column
        // when the axis reports `isFromZero` (its right-side/outside default):
        // x = 0 with align "left", i.e. the pill starts at the plot border. The
        // +gutter shift moves it into the canvas, adding left padding and eating
        // into the axis' marginEnd (8px), so a 6px shift still leaves ~2px of
        // right margin. For the inside/left axis the pill is right-aligned on
        // the canvas edge, so the same shift goes negative and buys right
        // padding. Either way the tag keeps clear of the canvas border.
        let fromZero = false;
        try {
          fromZero = Boolean(this.getWidget?.().getAxisComponent?.().isFromZero?.());
        } catch (_) {}
        const shift = fromZero ? RIGHT_AXIS_GUTTER : -RIGHT_AXIS_GUTTER;
        ctx.save();
        ctx.translate(shift, 0);
        try {
          return drawImp.call(this, ctx);
        } finally {
          ctx.restore();
        }
      };
    }
  } catch (error) {
    console.warn("chart axis gutter patch failed", error);
  }
}

let xAxisTickPatched = false;
/**
 * x-axis time labels are centred on their tick and have no edge guard, so the
 * newest label is clipped (`15:0…`). Pull the first/last label inside the plot
 * and right/left-align it instead of letting it run past the canvas.
 */
function patchXAxisTickLabels(chart) {
  if (xAxisTickPatched) return;
  try {
    const view = chart?.getXAxisPane?.()?._mainWidget?._xAxisView;
    const proto = view ? Object.getPrototypeOf(view) : null;
    if (!proto || typeof proto.createTickTexts !== "function") return;
    xAxisTickPatched = true;

    const createTickTexts = proto.createTickTexts;
    proto.createTickTexts = function(ticks, bounding, styles) {
      const attrs = createTickTexts.call(this, ticks, bounding, styles);
      const width = Number(bounding?.width) || 0;
      if (!Array.isArray(attrs) || width <= 0) return attrs;
      const text = styles?.tickText || {};
      for (const attr of attrs) {
        if (!attr || !Number.isFinite(attr.x)) continue;
        const half = measureTextWidth(attr.text, text.size, text.weight, text.family) / 2;
        if (attr.x + half > width - X_AXIS_EDGE_GUTTER) {
          attr.x = width - X_AXIS_EDGE_GUTTER;
          attr.align = "right";
        } else if (attr.x - half < X_AXIS_EDGE_GUTTER) {
          attr.x = X_AXIS_EDGE_GUTTER;
          attr.align = "left";
        }
      }
      return attrs;
    };
  } catch (error) {
    console.warn("chart x-axis gutter patch failed", error);
  }
}

function calculateSRLevels(candles, maxLevels = 3) {
  if (!Array.isArray(candles) || candles.length < 5) {
    return { supports: [], resistances: [] };
  }
  const currentPrice = candles[candles.length - 1].close;
  const highs = [];
  const lows = [];
  const windowSize = 2;

  for (let i = windowSize; i < candles.length - windowSize; i++) {
    const c = candles[i];
    let isHigh = true;
    let isLow = true;
    for (let j = i - windowSize; j <= i + windowSize; j++) {
      if (j === i) continue;
      if (candles[j].high > c.high) isHigh = false;
      if (candles[j].low < c.low) isLow = false;
    }
    if (isHigh) highs.push({ price: c.high, time: c.time });
    if (isLow) lows.push({ price: c.low, time: c.time });
  }

  function cluster(points) {
    if (!points.length) return [];
    const sorted = [...points].sort((a, b) => a.price - b.price);
    const clusters = [];
    for (const p of sorted) {
      const match = clusters.find((cl) => Math.abs(cl.avg - p.price) / cl.avg < 0.006);
      if (match) {
        match.prices.push(p.price);
        match.count += 1;
        match.avg = match.prices.reduce((a, b) => a + b, 0) / match.prices.length;
        match.latestTime = Math.max(match.latestTime, p.time);
      } else {
        clusters.push({ avg: p.price, prices: [p.price], count: 1, latestTime: p.time });
      }
    }
    return clusters;
  }

  const resClusters = cluster(highs.filter((h) => h.price > currentPrice));
  const supClusters = cluster(lows.filter((l) => l.price < currentPrice));

  resClusters.sort((a, b) => a.avg - b.avg);
  supClusters.sort((a, b) => b.avg - a.avg);

  return {
    resistances: resClusters.slice(0, maxLevels).map((c) => Number(c.avg.toFixed(2))),
    supports: supClusters.slice(0, maxLevels).map((c) => Number(c.avg.toFixed(2))),
  };
}

export function calculateAutoTrendlines(candles, maxLookback = 120) {
  if (!Array.isArray(candles) || candles.length < 15) {
    return { resistance: null, support: null };
  }
  const slice = candles.length > maxLookback ? candles.slice(-maxLookback) : candles;
  const n = slice.length;
  const lastIndex = n - 1;
  const currentPrice = slice[lastIndex].close;
  if (!Number.isFinite(currentPrice) || currentPrice <= 0) {
    return { resistance: null, support: null };
  }

  const windowSize = 2;
  const highs = [];
  const lows = [];

  for (let i = windowSize; i < n - windowSize; i++) {
    const c = slice[i];
    let isHigh = true;
    let isLow = true;
    for (let j = i - windowSize; j <= i + windowSize; j++) {
      if (j === i) continue;
      if (slice[j].high > c.high) isHigh = false;
      if (slice[j].low < c.low) isLow = false;
    }
    if (isHigh) highs.push({ index: i, price: c.high, time: c.time });
    if (isLow) lows.push({ index: i, price: c.low, time: c.time });
  }

  const touchTol = Math.max(0.0006, Math.min(0.006, 1.2 / currentPrice));
  const breachTol = touchTol * 1.6;

  // Resistance: Connect prominent swing highs
  let bestResistance = null;
  let bestResScore = -Infinity;

  for (let a = 0; a < highs.length; a++) {
    for (let b = a + 1; b < highs.length; b++) {
      const p1 = highs[a];
      const p2 = highs[b];
      const span = p2.index - p1.index;
      if (span < 5) continue;

      const slope = (p2.price - p1.price) / span;
      const lastLinePrice = p1.price + slope * (lastIndex - p1.index);

      // Resistance must remain at or above current prices
      if (lastLinePrice < currentPrice * (1 - breachTol * 2)) continue;
      // Not excessively far above current price (keep relevant)
      if (lastLinePrice > currentPrice * 1.25) continue;

      let breaches = 0;
      let wickBreaches = 0;
      let extraTouches = 0;
      let lastTouchIdx = -999;

      for (let k = p1.index; k <= lastIndex; k++) {
        if (k === p1.index || k === p2.index) continue;
        const linePx = p1.price + slope * (k - p1.index);
        const bar = slice[k];

        if (bar.close > linePx * (1 + breachTol)) {
          breaches++;
          break;
        }
        if (bar.high > linePx * (1 + breachTol * 2)) {
          wickBreaches++;
          break;
        }
        if (Math.abs(bar.high - linePx) / linePx <= touchTol && bar.close <= linePx * (1 + touchTol * 0.5)) {
          if (k - lastTouchIdx >= 3) {
            extraTouches++;
            lastTouchIdx = k;
          }
        }
      }

      if (breaches > 0 || wickBreaches > 0) continue;

      const totalTouches = 2 + extraTouches;
      const recency = p2.index / lastIndex;
      const distPct = Math.abs(lastLinePrice - currentPrice) / currentPrice;
      const score = (totalTouches * 20) + (span * 0.3) + (recency * 20) - (distPct * 60);

      if (score > bestResScore) {
        bestResScore = score;
        bestResistance = {
          p1: { time: p1.time, price: p1.price },
          p2: { time: p2.time, price: p2.price },
          span,
          touches: totalTouches,
          score: Number(score.toFixed(1)),
          slope: Number(slope.toFixed(4)),
          projectedPrice: Number(lastLinePrice.toFixed(2)),
        };
      }
    }
  }

  // Support: Connect prominent swing lows
  let bestSupport = null;
  let bestSupScore = -Infinity;

  for (let a = 0; a < lows.length; a++) {
    for (let b = a + 1; b < lows.length; b++) {
      const p1 = lows[a];
      const p2 = lows[b];
      const span = p2.index - p1.index;
      if (span < 5) continue;

      const slope = (p2.price - p1.price) / span;
      const lastLinePrice = p1.price + slope * (lastIndex - p1.index);

      // Support must remain at or below current prices
      if (lastLinePrice > currentPrice * (1 + breachTol * 2)) continue;
      // Not excessively far below current price
      if (lastLinePrice < currentPrice * 0.75) continue;

      let breaches = 0;
      let wickBreaches = 0;
      let extraTouches = 0;
      let lastTouchIdx = -999;

      for (let k = p1.index; k <= lastIndex; k++) {
        if (k === p1.index || k === p2.index) continue;
        const linePx = p1.price + slope * (k - p1.index);
        const bar = slice[k];

        if (bar.close < linePx * (1 - breachTol)) {
          breaches++;
          break;
        }
        if (bar.low < linePx * (1 - breachTol * 2)) {
          wickBreaches++;
          break;
        }
        if (Math.abs(bar.low - linePx) / linePx <= touchTol && bar.close >= linePx * (1 - touchTol * 0.5)) {
          if (k - lastTouchIdx >= 3) {
            extraTouches++;
            lastTouchIdx = k;
          }
        }
      }

      if (breaches > 0 || wickBreaches > 0) continue;

      const totalTouches = 2 + extraTouches;
      const recency = p2.index / lastIndex;
      const distPct = Math.abs(currentPrice - lastLinePrice) / currentPrice;
      const score = (totalTouches * 20) + (span * 0.3) + (recency * 20) - (distPct * 60);

      if (score > bestSupScore) {
        bestSupScore = score;
        bestSupport = {
          p1: { time: p1.time, price: p1.price },
          p2: { time: p2.time, price: p2.price },
          span,
          touches: totalTouches,
          score: Number(score.toFixed(1)),
          slope: Number(slope.toFixed(4)),
          projectedPrice: Number(lastLinePrice.toFixed(2)),
        };
      }
    }
  }

  return { resistance: bestResistance, support: bestSupport };
}

function buildStyles(colors) {
  return {
    grid: {
      show: true,
      horizontal: { show: true, size: 1, color: colors.grid, style: "dashed", dashedValue: [2, 2] },
      vertical: { show: true, size: 1, color: colors.grid, style: "dashed", dashedValue: [2, 2] },
    },
    candle: {
      type: "candle_solid",
      bar: {
        upColor: colors.up,
        downColor: colors.down,
        noChangeColor: colors.text,
        upBorderColor: colors.up,
        downBorderColor: colors.down,
        noChangeBorderColor: colors.text,
        upWickColor: colors.up,
        downWickColor: colors.down,
        noChangeWickColor: colors.text,
      },
      priceMark: {
        show: true,
        high: { show: true, color: colors.text, textSize: 10 },
        low: { show: true, color: colors.text, textSize: 10 },
        last: {
          show: true,
          upColor: colors.up,
          downColor: colors.down,
          noChangeColor: colors.text,
          line: { show: true, style: "dashed", dashedValue: [4, 4], size: 1, color: colors.crosshair },
          text: { show: true, color: "#ffffff", backgroundColor: colors.up, size: 11 },
        },
      },
      tooltip: {
        showRule: "follow_cross",
        showType: "standard",
        text: { size: 11, color: colors.text },
      },
    },
    indicator: {
      lastValueMark: {
        show: true,
        text: {
          show: true,
          style: "fill",
          color: "#ffffff",
          size: 11,
          weight: "bold",
          paddingLeft: 4,
          paddingTop: 3,
          paddingRight: 4,
          paddingBottom: 3,
          borderRadius: 2,
        },
      },
      lines: [
        { style: "solid", smooth: false, size: 1, dashedValue: [2, 2], color: "#e16a2b" },
        { style: "solid", smooth: false, size: 1, dashedValue: [2, 2], color: "#2196f3" },
        { style: "solid", smooth: false, size: 1, dashedValue: [2, 2], color: "#9c27b0" },
        { style: "solid", smooth: false, size: 1, dashedValue: [2, 2], color: "#00b4d8" },
      ],
      bars: [
        {
          style: "fill",
          upColor: `${colors.up}80`,
          downColor: `${colors.down}80`,
          noChangeColor: `${colors.text}80`,
        },
      ],
      tooltip: {
        showRule: "always",
        showType: "standard",
        title: {
          show: true,
          showName: true,
          showParams: true,
          size: 11,
          color: colors.text,
          marginLeft: 8,
          marginTop: 4,
          marginRight: 8,
          marginBottom: 4,
        },
        legend: {
          size: 11,
          color: colors.text,
          marginLeft: 8,
          marginTop: 4,
          marginRight: 8,
          marginBottom: 4,
        },
      },
    },
    xAxis: {
      axisLine: { show: true, size: 1, color: colors.border },
      tickLine: { show: true, size: 1, color: colors.border },
      tickText: { show: true, color: colors.text, size: 11, marginStart: 4 },
    },
    yAxis: {
      axisLine: { show: true, size: 1, color: colors.border },
      tickLine: { show: true, size: 1, color: colors.border },
      // marginEnd is the gap between the last price glyph and the canvas edge.
      tickText: { show: true, color: colors.text, size: 11, marginStart: 4, marginEnd: 8 },
    },
    separator: { size: 1, color: colors.border },
    crosshair: {
      show: true,
      horizontal: {
        show: true,
        line: { show: true, style: "dashed", dashedValue: [4, 2], size: 1, color: colors.crosshair },
        text: { show: true, color: "#ffffff", backgroundColor: colors.crosshair, size: 11 },
      },
      vertical: {
        show: true,
        line: { show: true, style: "dashed", dashedValue: [4, 2], size: 1, color: colors.crosshair },
        text: { show: true, color: "#ffffff", backgroundColor: colors.crosshair, size: 11 },
      },
    },
    overlay: {
      line: { color: colors.crosshair, size: 1.5 },
      point: { color: colors.up, radius: 4 },
    },
  };
}

export function parseTimestamp(ts) {
  if (!ts) return 0;
  if (typeof ts === "number") return ts < 1e11 ? ts * 1000 : ts;
  const numVal = Number(ts);
  if (Number.isFinite(numVal) && numVal > 0) return numVal < 1e11 ? numVal * 1000 : numVal;
  const str = String(ts).trim();
  const m = str.match(/^(\d{1,2})[-\/]([A-Za-z]{3}|\d{1,2})[-\/](\d{4})(?:\s+(\d{1,2}):(\d{2})(?::(\d{2}))?)?/);
  if (m) {
    const months: Record<string, number> = { jan: 0, feb: 1, mar: 2, apr: 3, may: 4, jun: 5, jul: 6, aug: 7, sep: 8, oct: 9, nov: 10, dec: 11 };
    const day = parseInt(m[1], 10);
    const mon = isNaN(Number(m[2])) ? (months[m[2].toLowerCase()] ?? 0) : parseInt(m[2], 10) - 1;
    const year = parseInt(m[3], 10);
    const hour = parseInt(m[4] || "0", 10);
    const min = parseInt(m[5] || "0", 10);
    const sec = parseInt(m[6] || "0", 10);
    return new Date(year, mon, day, hour, min, sec).getTime();
  }
  const d = new Date(str);
  return Number.isNaN(d.getTime()) ? 0 : d.getTime();
}

export function pairTrades(trades) {
  if (!Array.isArray(trades) || trades.length < 2) return [];
  const sorted = [...trades].sort((a, b) => parseTimestamp(a.timestamp) - parseTimestamp(b.timestamp));
  const pairs = [];
  const openLots = [];

  for (const t of sorted) {
    const isBuy = t.side === "B" || t.side === "BUY";
    let curQty = Math.abs(Number(t.quantity || 0));
    if (curQty <= 0) continue;

    while (curQty > 0 && openLots.length > 0) {
      const opp = openLots[0];
      const oppIsBuy = opp.trade.side === "B" || opp.trade.side === "BUY";
      if (oppIsBuy === isBuy) break;

      const matchQty = Math.min(curQty, opp.remainingQty);
      const entry = opp.trade;
      const exit = t;
      const entryPx = Number(entry.price || 0);
      const exitPx = Number(exit.price || 0);
      const pnl = oppIsBuy ? (exitPx - entryPx) * matchQty : (entryPx - exitPx) * matchQty;
      const pnlPct = entryPx > 0 ? (oppIsBuy ? ((exitPx - entryPx) / entryPx) * 100 : ((entryPx - exitPx) / entryPx) * 100) : 0;

      pairs.push({
        entry,
        exit,
        qty: matchQty,
        pnl,
        pnlPct,
      });

      curQty -= matchQty;
      opp.remainingQty -= matchQty;
      if (opp.remainingQty <= 0) openLots.shift();
    }

    if (curQty > 0) {
      openLots.push({ trade: t, remainingQty: curQty });
    }
  }
  return pairs;
}

function findClosestCandle(candles, timestampMs) {
  if (!Array.isArray(candles) || !candles.length) return null;
  const tsSec = timestampMs > 1e11 ? Math.floor(timestampMs / 1000) : timestampMs;
  let best = candles[0];
  let minDiff = Math.abs(candles[0].time - tsSec);
  for (let i = 1; i < candles.length; i++) {
    const diff = Math.abs(candles[i].time - tsSec);
    if (diff < minDiff) {
      minDiff = diff;
      best = candles[i];
    } else if (diff > minDiff && candles[i].time > tsSec) {
      break;
    }
  }
  return best;
}

export function formatDOMQty(q: number | string): string {
  const n = Number(q || 0);
  if (n <= 0) return "";
  if (n >= 1e7) return `${(n / 1e7).toFixed(1)}Cr`;
  if (n >= 1e5) return `${(n / 1e5).toFixed(1)}L`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}K`;
  return String(Math.round(n));
}

export function computeDOMInterval(minPrice: number, maxPrice: number, heightPx = 500): number {
  const span = maxPrice - minPrice;
  const isSpotOrLarge = minPrice > 5000;
  if (!Number.isFinite(span) || span <= 0) return isSpotOrLarge ? 10 : 5;

  const desiredRows = Math.max(6, Math.min(20, Math.floor(heightPx / 28)));
  const rawStep = span / desiredRows;

  const candidateSteps = isSpotOrLarge
    ? [5, 10, 20, 25, 50, 100, 200, 500]
    : [0.5, 1, 2, 2.5, 5, 10, 20, 25, 50, 100];

  for (const step of candidateSteps) {
    if (step >= rawStep) {
      return step;
    }
  }
  return candidateSteps[candidateSteps.length - 1];
}

export function calculateOrderFlowBuckets(candles: any[], depthData: any, chainData: any[], minPrice: number, maxPrice: number, step: number) {
  const buckets = new Map();

  const startP = Math.floor((minPrice - step) / step) * step;
  const endP = Math.ceil((maxPrice + step) / step) * step;

  for (let p = startP; p <= endP + 1e-6; p = Math.round((p + step) * 100) / 100) {
    const key = Math.round(p * 100) / 100;
    buckets.set(key, {
      price: key,
      buyVol: 0,
      sellVol: 0,
      buyOrders: 0,
      sellOrders: 0,
    });
  }

  const recent = Array.isArray(candles) ? candles.slice(-80) : [];
  for (const c of recent) {
    const low = Number(c.low);
    const high = Number(c.high);
    const open = Number(c.open);
    const close = Number(c.close);
    const vol = Number(c.volume || 0);

    if (!low || !high || high <= 0 || low <= 0) continue;
    const range = Math.max(0.01, high - low);

    let buyRatio = 0.5;
    if (high > low) {
      buyRatio = Math.max(0.1, Math.min(0.9, (close - low) / range));
    } else if (close > open) {
      buyRatio = 0.75;
    } else if (close < open) {
      buyRatio = 0.25;
    }

    const cBuyVol = vol > 0 ? vol * buyRatio : (buyRatio >= 0.5 ? Math.round(buyRatio * 25) : Math.round(buyRatio * 8));
    const cSellVol = vol > 0 ? vol * (1 - buyRatio) : (buyRatio < 0.5 ? Math.round((1 - buyRatio) * 25) : Math.round((1 - buyRatio) * 8));
    const cBuyOrd = buyRatio >= 0.5 ? Math.max(1, Math.round(buyRatio * 6)) : 1;
    const cSellOrd = (1 - buyRatio) >= 0.5 ? Math.max(1, Math.round((1 - buyRatio) * 6)) : 1;

    const bStart = Math.floor(low / step) * step;
    const bEnd = Math.ceil(high / step) * step;

    for (let p = bStart; p <= bEnd + 1e-6; p = Math.round((p + step) * 100) / 100) {
      const bKey = Math.round(p * 100) / 100;
      const b = buckets.get(bKey);
      if (!b) continue;

      const overlapLow = Math.max(low, p - step / 2);
      const overlapHigh = Math.min(high, p + step / 2);
      const overlapLen = Math.max(0, overlapHigh - overlapLow);
      const weight = overlapLen / range;

      if (weight > 0) {
        b.buyVol += cBuyVol * weight;
        b.sellVol += cSellVol * weight;
        b.buyOrders += cBuyOrd * weight;
        b.sellOrders += cSellOrd * weight;
      }
    }
  }

  if (Array.isArray(chainData) && chainData.length) {
    for (const row of chainData) {
      const strike = Number(row.strike || 0);
      if (strike <= 0) continue;
      const bKey = Math.round(Math.round(strike / step) * step * 100) / 100;
      const b = buckets.get(bKey);
      if (!b) continue;

      const callVol = Number(row.call?.volume || 0);
      const putVol = Number(row.put?.volume || 0);
      const callOI = Number(row.call?.oi || 0);
      const putOI = Number(row.put?.oi || 0);

      const spotBull = callVol + (putOI * 0.1);
      const spotBear = putVol + (callOI * 0.1);

      if (spotBull > 0) {
        b.buyVol += spotBull * 0.2;
        b.buyOrders += Math.max(1, Math.round(spotBull / 500));
      }
      if (spotBear > 0) {
        b.sellVol += spotBear * 0.2;
        b.sellOrders += Math.max(1, Math.round(spotBear / 500));
      }
    }
  }

  if (depthData) {
    const bids = Array.isArray(depthData.bids) ? depthData.bids : [];
    const asks = Array.isArray(depthData.asks) ? depthData.asks : [];

    for (const bid of bids) {
      const px = Number(bid.price || 0);
      const qty = Number(bid.quantity || 0);
      const ord = Number(bid.orders || 1);
      if (px <= 0 || qty <= 0) continue;
      const bKey = Math.round(Math.round(px / step) * step * 100) / 100;
      let b = buckets.get(bKey);
      if (!b && bKey >= startP && bKey <= endP) {
        b = { price: bKey, buyVol: 0, sellVol: 0, buyOrders: 0, sellOrders: 0 };
        buckets.set(bKey, b);
      }
      if (b) {
        b.buyVol += qty;
        b.buyOrders += ord;
      }
    }

    for (const ask of asks) {
      const px = Number(ask.price || 0);
      const qty = Number(ask.quantity || 0);
      const ord = Number(ask.orders || 1);
      if (px <= 0 || qty <= 0) continue;
      const bKey = Math.round(Math.round(px / step) * step * 100) / 100;
      let b = buckets.get(bKey);
      if (!b && bKey >= startP && bKey <= endP) {
        b = { price: bKey, buyVol: 0, sellVol: 0, buyOrders: 0, sellOrders: 0 };
        buckets.set(bKey, b);
      }
      if (b) {
        b.sellVol += qty;
        b.sellOrders += ord;
      }
    }
  }

  const result = [];
  let maxVol = 1;
  for (const b of buckets.values()) {
    b.buyVol = Math.round(b.buyVol);
    b.sellVol = Math.round(b.sellVol);
    b.buyOrders = Math.round(b.buyOrders);
    b.sellOrders = Math.round(b.sellOrders);
    const total = Math.max(b.buyVol, b.sellVol);
    if (total > maxVol) maxVol = total;
    if (b.buyVol > 0 || b.sellVol > 0) {
      result.push(b);
    }
  }

  return { buckets: result, maxVol, step };
}

let overlaysRegistered = false;
function registerCustomOverlays() {
  const kc = window.klinecharts;
  if (!kc || overlaysRegistered) return;
  overlaysRegistered = true;

  // Vertical marker at each trading-day boundary. Bars are spaced by index, so
  // a new day is otherwise only visible in the x-axis labels.
  kc.registerOverlay({
    name: "dayDividerLine",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.x)) return [];
      const data = overlay.extendData || {};
      const color = data.color || "#536a7d";
      const height = bounding?.height || 400;
      return [{
        type: "line",
        attrs: { coordinates: [{ x: coord.x, y: 0 }, { x: coord.x, y: height }] },
        styles: { style: "dashed", dashedValue: [2, 4], size: 1, color },
        ignoreEvent: true,
      }];
    },
  });

  kc.registerOverlay({
    name: "tradeMarker",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.x) || !Number.isFinite(coord.y)) return [];

      const data = overlay.extendData || {};
      const isBuy = data.side === "B" || data.side === "BUY" || data.isBull;
      const isSpot = Boolean(data.isSpot);

      const color = isBuy ? "#29b360" : "#f05a5a";
      const figures = [];

      if (isSpot) {
        const arrowUp = isBuy;
        const tipY = arrowUp ? coord.y + 4 : coord.y - 4;
        const baseY = arrowUp ? coord.y + 12 : coord.y - 12;
        const textY = arrowUp ? coord.y + 14 : coord.y - 14;
        const baseline = arrowUp ? "top" : "bottom";

        figures.push({
          type: "polygon",
          attrs: {
            coordinates: [
              { x: coord.x, y: tipY },
              { x: coord.x - 5, y: baseY },
              { x: coord.x + 5, y: baseY },
            ],
          },
          styles: { style: "fill", color },
          ignoreEvent: true,
        });

        const label = data.text || (arrowUp ? "▲ BUY" : "▼ SELL");
        const labelStyles = {
          color: "#ffffff",
          backgroundColor: color,
          size: 10,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 5,
          paddingRight: 5,
          paddingTop: 1.5,
          paddingBottom: 1.5,
          borderRadius: 3,
        };
        figures.push({
          type: "text",
          attrs: clampTextFigureX({
            x: coord.x,
            y: textY,
            text: label,
            align: "center",
            baseline,
          }, labelStyles, bounding?.width),
          styles: labelStyles,
          ignoreEvent: true,
        });
      } else {
        const tipY = isBuy ? coord.y + 3 : coord.y - 3;
        const baseY = isBuy ? coord.y + 11 : coord.y - 11;
        const textY = isBuy ? coord.y + 13 : coord.y - 13;
        const baseline = isBuy ? "top" : "bottom";

        figures.push({
          type: "polygon",
          attrs: {
            coordinates: [
              { x: coord.x, y: tipY },
              { x: coord.x - 4.5, y: baseY },
              { x: coord.x + 4.5, y: baseY },
            ],
          },
          styles: { style: "fill", color },
          ignoreEvent: true,
        });

        const sideStr = isBuy ? "BUY" : "SELL";
        const qtyStr = data.qty ? `${data.qty} @ ` : "";
        const priceStr = Number.isFinite(data.price) ? Number(data.price).toFixed(2) : "";
        const label = `${sideStr} ${qtyStr}${priceStr}`.trim();

        figures.push({
          type: "text",
          attrs: clampTextFigureX({
            x: coord.x,
            y: textY,
            text: label,
            align: "center",
            baseline,
          }, { size: 10, weight: "bold", paddingLeft: 5, paddingRight: 5, borderSize: 0 }, bounding?.width),
          styles: {
            color: "#ffffff",
            backgroundColor: color,
            size: 10,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 5,
            paddingRight: 5,
            paddingTop: 1.5,
            paddingBottom: 1.5,
            borderRadius: 3,
          },
          ignoreEvent: true,
        });
      }

      return figures;
    },
  });

  kc.registerOverlay({
    name: "depthLevelBar",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding, yAxis }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.y)) return [];
      const y = coord.y;
      const height = bounding?.height || 500;
      if (y < -20 || y > height + 20) return [];

      const width = bounding?.width || 800;
      const data = overlay.extendData || {};

      // ── Anchored Dual-Sided DOM Row (Fixed Interval) ──
      if (data.buyVol != null || data.sellVol != null) {
        const buyVol = Number(data.buyVol || 0);
        const sellVol = Number(data.sellVol || 0);
        const maxVol = Number(data.maxVol || Math.max(buyVol, sellVol, 1));

        const layout = depthLadderLayout(width, axisPaneWidth(yAxis, width));
        const step = Number(data.step) || 0;
        // Row height follows the real pixel distance between two ladder steps
        // so bubbles never overlap when the axis is zoomed out on a short pane.
        let rowPx = 0;
        if (step > 0 && yAxis && typeof yAxis.convertToPixel === "function") {
          const here = Number(yAxis.convertToPixel(Number(data.price)));
          const next = Number(yAxis.convertToPixel(Number(data.price) - step));
          if (Number.isFinite(here) && Number.isFinite(next)) rowPx = Math.abs(next - here);
        }
        const barHeight = rowPx > 0
          ? Math.max(7, Math.min(14, Math.round(rowPx) - 4))
          : 14;
        const yTop = y - barHeight / 2;
        const yBottom = y + barHeight / 2;

        const figures = [];

        if (layout.compact) {
          // Compact strip: just the two volumes per row, right aligned. No wide
          // panel background, so the candles behind stay visible.
          const xRight = layout.right;
          const xLeft = xRight - layout.panelWidth;
          const xCenter = Math.round(xLeft + layout.panelWidth * 0.5);
          const pill = {
            paddingLeft: 3,
            paddingRight: 3,
            paddingTop: 1,
            paddingBottom: 1,
            borderSize: 0,
            borderRadius: 2,
            size: 9,
            weight: "bold",
            family: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
          };
          const rowColors = data.rowColors || domRowColors();

          if (buyVol > 0) {
            figures.push({
              type: "text",
              attrs: {
                x: xCenter - 2,
                y,
                text: formatDOMQty(buyVol),
                align: "right",
                baseline: "middle",
              },
              styles: Object.assign({}, pill, {
                color: rowColors.buyText,
                backgroundColor: rowColors.buyPill,
              }),
              ignoreEvent: true,
            });
          }
          if (sellVol > 0) {
            figures.push({
              type: "text",
              attrs: {
                x: xCenter + 2,
                y,
                text: formatDOMQty(sellVol),
                align: "left",
                baseline: "middle",
              },
              styles: Object.assign({}, pill, {
                color: rowColors.sellText,
                backgroundColor: rowColors.sellPill,
              }),
              ignoreEvent: true,
            });
          }
          return figures;
        }

        const xRight = layout.right;
        const xLeft = layout.left;
        const xCenter = Math.round(xLeft + layout.panelWidth * 0.5);
        const halfWidth = Math.round((layout.panelWidth - 14) * 0.48);

        // Background container pill for readability
        figures.push({
          type: "polygon",
          attrs: {
            coordinates: [
              { x: xLeft, y: yTop },
              { x: xRight, y: yTop },
              { x: xRight, y: yBottom },
              { x: xLeft, y: yBottom },
            ],
          },
          styles: { style: "fill", color: (data.rowColors || domRowColors()).panelBg },
          ignoreEvent: true,
        });

        // Center balance line / tick
        figures.push({
          type: "line",
          attrs: {
            coordinates: [
              { x: xCenter, y: yTop - 1 },
              { x: xCenter, y: yBottom + 1 },
            ],
          },
          styles: { style: "solid", size: 1.5, color: (data.rowColors || domRowColors()).spine },
          ignoreEvent: true,
        });

        // 1. Buyers (Green bar on left of center)
        if (buyVol > 0) {
          const buyRatio = Math.max(0.05, Math.min(1.0, buyVol / maxVol));
          const buyBarW = Math.max(6, Math.round(buyRatio * halfWidth));
          const xBuyRight = xCenter - 3;
          const xBuyLeft = xBuyRight - buyBarW;

          figures.push({
            type: "polygon",
            attrs: {
              coordinates: [
                { x: xBuyLeft, y: yTop },
                { x: xBuyRight, y: yTop },
                { x: xBuyRight, y: yBottom },
                { x: xBuyLeft, y: yBottom },
              ],
            },
            styles: { style: "fill", color: "rgba(34, 197, 94, 0.35)" },
            ignoreEvent: true,
          });

          figures.push({
            type: "line",
            attrs: {
              coordinates: [
                { x: xBuyLeft, y: yTop },
                { x: xBuyLeft, y: yBottom },
              ],
            },
            styles: { style: "solid", size: 2.5, color: "#22c55e" },
            ignoreEvent: true,
          });

          const buyText = formatDOMQty(buyVol);
          figures.push({
            type: "text",
            attrs: {
              x: xBuyRight - 4,
              y,
              text: buyText,
              align: "right",
              baseline: "middle",
            },
            styles: {
              color: (data.rowColors || domRowColors()).buyText,
              backgroundColor: (data.rowColors || domRowColors()).buyPill,
              size: 9,
              weight: "bold",
              paddingLeft: 3,
              paddingRight: 3,
              paddingTop: 1,
              paddingBottom: 1,
              borderSize: 0,
              borderRadius: 2,
              family: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
            },
            ignoreEvent: true,
          });
        }

        // 2. Sellers (Red bar on right of center)
        if (sellVol > 0) {
          const sellRatio = Math.max(0.05, Math.min(1.0, sellVol / maxVol));
          const sellBarW = Math.max(6, Math.round(sellRatio * halfWidth));
          const xSellLeft = xCenter + 3;
          const xSellRight = xSellLeft + sellBarW;

          figures.push({
            type: "polygon",
            attrs: {
              coordinates: [
                { x: xSellLeft, y: yTop },
                { x: xSellRight, y: yTop },
                { x: xSellRight, y: yBottom },
                { x: xSellLeft, y: yBottom },
              ],
            },
            styles: { style: "fill", color: "rgba(239, 68, 68, 0.35)" },
            ignoreEvent: true,
          });

          figures.push({
            type: "line",
            attrs: {
              coordinates: [
                { x: xSellRight, y: yTop },
                { x: xSellRight, y: yBottom },
              ],
            },
            styles: { style: "solid", size: 2.5, color: "#ef4444" },
            ignoreEvent: true,
          });

          const sellText = formatDOMQty(sellVol);
          figures.push({
            type: "text",
            attrs: {
              x: xSellLeft + 4,
              y,
              text: sellText,
              align: "left",
              baseline: "middle",
            },
            styles: {
              color: (data.rowColors || domRowColors()).sellText,
              backgroundColor: (data.rowColors || domRowColors()).sellPill,
              size: 9,
              weight: "bold",
              paddingLeft: 3,
              paddingRight: 3,
              paddingTop: 1,
              paddingBottom: 1,
              borderSize: 0,
              borderRadius: 2,
              family: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
            },
            ignoreEvent: true,
          });
        }

        return figures;
      }

      // Single-sided fallback
      const isBuy = data.side === "BUY" || data.side === "B";
      const orders = Number(data.orders || 0);
      const qty = Number(data.quantity || 0);
      const maxQty = Number(data.maxQty || qty || 1);

      const baseColor = isBuy ? "#29b360" : "#f05a5a";
      const fillColor = isBuy ? "rgba(41, 179, 96, 0.35)" : "rgba(240, 90, 90, 0.35)";
      const layout = depthLadderLayout(width, axisPaneWidth(yAxis, width));
      const step = Number(data.step) || 0;
      let rowPx = 0;
      if (step > 0 && yAxis && typeof yAxis.convertToPixel === "function") {
        const here = Number(yAxis.convertToPixel(Number(data.price)));
        const next = Number(yAxis.convertToPixel(Number(data.price) - step));
        if (Number.isFinite(here) && Number.isFinite(next)) rowPx = Math.abs(next - here);
      }
      const barHeight = rowPx > 0 ? Math.max(8, Math.min(16, Math.round(rowPx) - 4)) : 16;

      const ratio = Math.max(0.08, Math.min(1.0, qty / (maxQty || 1)));
      const maxBarWidth = Math.min(220, Math.max(90, width * 0.26));
      const barWidth = Math.round(ratio * maxBarWidth);

      const x2 = layout.right;
      const x1 = x2 - barWidth;
      const yTop = y - barHeight / 2;
      const yBottom = y + barHeight / 2;

      const figures = [];

      figures.push({
        type: "polygon",
        attrs: {
          coordinates: [
            { x: x1, y: yTop },
            { x: x2, y: yTop },
            { x: x2, y: yBottom },
            { x: x1, y: yBottom },
          ],
        },
        styles: { style: "fill", color: fillColor },
        ignoreEvent: true,
      });

      figures.push({
        type: "line",
        attrs: {
          coordinates: [
            { x: x1, y: yTop },
            { x: x1, y: yBottom },
          ],
        },
        styles: { style: "solid", size: 2.5, color: baseColor },
        ignoreEvent: true,
      });

      const orderLabel = orders > 0 ? `${orders}o · ` : "";
      const qtyLabel = formatDOMQty(qty) || String(qty);
      const label = `${orderLabel}${qtyLabel}`;

      figures.push({
        type: "text",
        attrs: {
          x: x2 - 5,
          y,
          text: label,
          align: "right",
          baseline: "middle",
        },
        styles: {
          color: "#ffffff",
          backgroundColor: isBuy ? "rgba(16, 80, 42, 0.88)" : "rgba(127, 29, 29, 0.88)",
          paddingLeft: 4,
          paddingRight: 4,
          paddingTop: 1,
          paddingBottom: 1,
          borderRadius: 2,
          size: 9,
          weight: "bold",
          family: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
        },
        ignoreEvent: true,
      });

      return figures;
    },
  });

  kc.registerOverlay({
    name: "positionLine",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.y)) return [];
      const y = coord.y;
      const data = overlay.extendData || {};
      const pnl = Number(data.pnl || 0);
      const isProfit = pnl >= 0;
      const lineColor = isProfit ? "#29b360" : "#f05a5a";
      const width = bounding?.width || 800;

      const figures = [];

      figures.push({
        type: "line",
        attrs: {
          coordinates: [
            { x: 0, y },
            { x: width, y },
          ],
        },
        styles: {
          style: "dashed",
          dashedValue: [5, 4],
          size: 1.5,
          color: lineColor,
        },
        ignoreEvent: true,
      });

      const qty = Number(data.qty || 0);
      const qtySign = qty > 0 ? "+" : "";
      const pnlSign = pnl >= 0 ? "+₹" : "-₹";
      const pnlVal = Math.abs(Math.round(pnl)).toLocaleString("en-IN");
      const avgPrice = Number(data.avgPrice || 0).toFixed(2);
      const tagText = `POS: ${qtySign}${qty} @ ${avgPrice} (${pnlSign}${pnlVal})`;

      figures.push({
        type: "text",
        attrs: {
          x: width - 8,
          y,
          text: tagText,
          align: "right",
          baseline: "middle",
        },
        styles: {
          color: "#ffffff",
          backgroundColor: lineColor,
          size: 10,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 6,
          paddingRight: 6,
          paddingTop: 2,
          paddingBottom: 2,
          borderRadius: 3,
        },
        ignoreEvent: true,
      });

      return figures;
    },
  });

  kc.registerOverlay({
    name: "tradeConnector",
    totalStep: 2,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates }) {
      if (!coordinates || coordinates.length < 2) return [];
      const c1 = coordinates[0];
      const c2 = coordinates[1];
      if (!c1 || !c2 || !Number.isFinite(c1.x) || !Number.isFinite(c1.y) ||
          !Number.isFinite(c2.x) || !Number.isFinite(c2.y)) return [];

      const data = overlay.extendData || {};
      const pnl = Number(data.pnl || 0);
      const isProfit = pnl >= 0;
      const color = isProfit ? "#29b36099" : "#f05a5a99";
      const midX = (c1.x + c2.x) / 2;
      const midY = (c1.y + c2.y) / 2;

      const figures = [];

      figures.push({
        type: "line",
        attrs: {
          coordinates: [
            { x: c1.x, y: c1.y },
            { x: c2.x, y: c2.y },
          ],
        },
        styles: {
          style: "dashed",
          dashedValue: [3, 3],
          size: 1.2,
          color,
        },
        ignoreEvent: true,
      });

      const pnlSign = pnl >= 0 ? "+₹" : "-₹";
      const pnlVal = Math.abs(Math.round(pnl)).toLocaleString("en-IN");
      const pctVal = Number.isFinite(data.pnlPct) ? ` (${data.pnlPct >= 0 ? "+" : ""}${data.pnlPct.toFixed(1)}%)` : "";
      const pillText = `${pnlSign}${pnlVal}${pctVal}`;

      figures.push({
        type: "text",
        attrs: {
          x: midX,
          y: midY,
          text: pillText,
          align: "center",
          baseline: "middle",
        },
        styles: {
          color: "#ffffff",
          backgroundColor: isProfit ? "#1e824c" : "#b03a2e",
          size: 9.5,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 4,
          paddingRight: 4,
          paddingTop: 1.5,
          paddingBottom: 1.5,
          borderRadius: 3,
        },
        ignoreEvent: true,
      });

      return figures;
    },
  });

  kc.registerOverlay({
    name: "maxPainLine",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding, yAxis }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.y)) return [];
      const y = coord.y;
      const data = overlay.extendData || {};
      const price = Number(data.price || overlay.points?.[0]?.value || 0);
      const width = bounding?.width || 800;
      const color = data.color || "#c084fc";
      // The DOM ladder owns the right strip of the plot; park the tag just to
      // its left so neither hides the other and neither is clipped.
      const x = data.depthVisible
        ? Math.max(8, depthLadderLayout(width, axisPaneWidth(yAxis, width)).left - 6)
        : Math.max(8, width - 8);
      return [
        {
          type: "line",
          attrs: { coordinates: [{ x: 0, y }, { x: width, y }] },
          styles: { style: "dashed", dashedValue: [6, 4], size: 1.2, color },
          ignoreEvent: true,
        },
        {
          type: "text",
          attrs: {
            x,
            y,
            text: `MP: ${Math.round(price).toLocaleString("en-IN")}`,
            align: "right",
            baseline: "middle",
          },
          styles: {
            color: "#0b0710",
            backgroundColor: color,
            size: 10,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 6,
            paddingRight: 6,
            paddingTop: 2,
            paddingBottom: 2,
            borderSize: 0,
            borderRadius: 3,
          },
          ignoreEvent: true,
        },
      ];
    },
  });

  kc.registerOverlay({
    name: "oiWallLine",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding, yAxis }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.y)) return [];
      const y = coord.y;
      const data = overlay.extendData || {};
      const width = bounding?.width || 800;
      const color = data.color || "#f0b429";
      const label = String(data.label || "");
      const labelOffset = Number(data.labelOffset) || 0;
      const x = data.depthVisible
        ? Math.max(8, depthLadderLayout(width, axisPaneWidth(yAxis, width)).left - 6)
        : Math.max(8, width - 8);
      return [
        {
          type: "line",
          attrs: { coordinates: [{ x: 0, y }, { x: width, y }] },
          styles: {
            style: data.metric === "doi" ? "dashed" : "solid",
            dashedValue: [3, 3],
            size: data.metric === "doi" ? 1 : 1.4,
            color,
          },
          ignoreEvent: true,
        },
        {
          type: "text",
          attrs: { x, y: y + labelOffset, text: label, align: "right", baseline: "middle" },
          styles: {
            color: "#0b0710",
            backgroundColor: color,
            size: 9.5,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 5,
            paddingRight: 5,
            paddingTop: 1.5,
            paddingBottom: 1.5,
            borderSize: 0,
            borderRadius: 3,
          },
          ignoreEvent: true,
        },
      ];
    },
  });

  kc.registerOverlay({
    name: "volumeProfile",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding, yAxis }) {
      const data = overlay.extendData || {};
      const nodes = Array.isArray(data.nodes) ? data.nodes : [];
      const priceHigh = Number(data.priceHigh);
      const priceLow = Number(data.priceLow);
      if (!nodes.length || !coordinates || coordinates.length < 2) return [];
      if (!Number.isFinite(priceHigh) || !Number.isFinite(priceLow) || priceHigh <= priceLow) return [];
      const top = coordinates[0];
      const bottom = coordinates[1];
      if (!Number.isFinite(top?.y) || !Number.isFinite(bottom?.y)) return [];

      const width = bounding?.width || 800;
      const span = priceHigh - priceLow;
      // Prefer the axis conversion: it stays correct on log/percentage scales.
      // The linear interpolation between the two anchor points is the fallback.
      const yOf = (price) => {
        const value = Number(price);
        if (!Number.isFinite(value)) return NaN;
        if (yAxis && typeof yAxis.convertToPixel === "function") {
          const pixel = Number(yAxis.convertToPixel(value));
          if (Number.isFinite(pixel)) return pixel;
        }
        return top.y + ((priceHigh - value) / span) * (bottom.y - top.y);
      };
      const palette = data.colors || {};
      const figures = [];

      // Zones first so the histogram bars sit on top of them. When the profile
      // says "wait", keep the shelves visible but barely tinted so they do not
      // read as an entry signal.
      const waiting = data.bias === "none";
      const band = (range, color, dimColor) => {
        if (!range) return;
        const y1 = yOf(Number(range.high));
        const y2 = yOf(Number(range.low));
        if (!Number.isFinite(y1) || !Number.isFinite(y2)) return;
        figures.push({
          type: "polygon",
          attrs: {
            coordinates: [
              { x: 0, y: y1 },
              { x: width, y: y1 },
              { x: width, y: y2 },
              { x: 0, y: y2 },
            ],
          },
          styles: {
            style: "fill",
            color: waiting ? dimColor : color,
            borderColor: "transparent",
            borderSize: 0,
          },
          ignoreEvent: true,
        });
      };
      band(data.buy, "rgba(41, 179, 96, 0.12)", "rgba(41, 179, 96, 0.04)");
      band(data.sell, "rgba(240, 90, 90, 0.12)", "rgba(240, 90, 90, 0.04)");

      let maxVolume = 0;
      for (const node of nodes) {
        if (Number(node?.volume) > maxVolume) maxVolume = Number(node.volume);
      }
      const maxBar = Math.max(20, width * 0.16);
      if (maxVolume > 0) {
        for (const node of nodes) {
          const volume = Number(node?.volume);
          if (!(volume > 0)) continue;
          const y1 = yOf(Number(node.high));
          const y2 = yOf(Number(node.low));
          if (!Number.isFinite(y1) || !Number.isFinite(y2)) continue;
          const barWidth = Math.max(1, (volume / maxVolume) * maxBar);
          const rising = Number(node.buy) >= Number(node.sell);
          figures.push({
            type: "polygon",
            attrs: {
              coordinates: [
                { x: width - barWidth, y: y1 },
                { x: width, y: y1 },
                { x: width, y: y2 },
                { x: width - barWidth, y: y2 },
              ],
            },
            styles: {
              style: "fill",
              color: rising ? "rgba(53, 196, 106, 0.30)" : "rgba(255, 93, 93, 0.30)",
              borderColor: "transparent",
              borderSize: 0,
            },
            ignoreEvent: true,
          });
        }
      }

      const levelColor = palette.valueArea || "rgba(148, 163, 184, 0.7)";
      const pocColor = palette.poc || "rgba(240, 180, 41, 0.95)";
      const level = (price, color, dashed, size) => {
        const value = Number(price);
        if (!Number.isFinite(value)) return;
        const y = yOf(value);
        figures.push({
          type: "line",
          attrs: { coordinates: [{ x: 0, y }, { x: width, y }] },
          styles: {
            style: dashed ? "dashed" : "solid",
            dashedValue: [5, 4],
            size,
            color,
          },
          ignoreEvent: true,
        });
      };
      level(data.val, levelColor, true, 1);
      level(data.vah, levelColor, true, 1);
      level(data.poc, pocColor, false, 1.4);

      const label = (price, text, color) => {
        const value = Number(price);
        if (!Number.isFinite(value)) return;
        figures.push({
          type: "text",
          attrs: { x: 8, y: yOf(value) - 5, text, align: "left", baseline: "bottom" },
          styles: {
            color,
            size: 9,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
          },
          ignoreEvent: true,
        });
      };
      label(data.poc, "POC", pocColor);
      label(data.vah, "VAH", levelColor);
      label(data.val, "VAL", levelColor);
      return figures;
    },
  });

  kc.registerOverlay({
    name: "fibRetracement",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const data = overlay.extendData || {};
      const levels = Array.isArray(data.levels) ? data.levels : [];
      if (!levels.length) return [];
      const width = bounding?.width || 800;
      const figures = [];
      const rows = [];
      for (let i = 0; i < levels.length; i++) {
        const level = levels[i];
        const coord = coordinates[i];
        if (!coord || !Number.isFinite(coord.y) || !Number.isFinite(level?.price)) continue;
        rows.push({ level, y: coord.y, labelY: coord.y });
      }
      const ordered = [...rows].sort((a, b) => a.y - b.y);
      let lastLabelY = -Infinity;
      for (const row of ordered) {
        row.labelY = Math.max(row.y, lastLabelY + 11);
        lastLabelY = row.labelY;
      }

      for (const row of rows) {
        const { level, y, labelY } = row;
        const color = level.color || "#2dd4bf";
        figures.push({
          type: "line",
          attrs: { coordinates: [{ x: 0, y }, { x: width, y }] },
          styles: {
            style: level.golden ? "solid" : "dashed",
            dashedValue: [4, 3],
            size: level.golden ? 1.4 : 1,
            color,
          },
          ignoreEvent: true,
        });
        figures.push({
          type: "text",
          attrs: {
            x: width - 8,
            y: labelY,
            text: `${level.label}  ${Math.round(level.price).toLocaleString("en-IN")}`,
            align: "right",
            baseline: "middle",
          },
          styles: {
            color: "#041014",
            backgroundColor: color,
            size: 9,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 5,
            paddingRight: 5,
            paddingTop: 1.5,
            paddingBottom: 1.5,
            borderRadius: 3,
          },
          ignoreEvent: true,
        });
      }
      return figures;
    },
  });

  kc.registerOverlay({
    name: "bracketLevelLine",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.y)) return [];
      const y = coord.y;
      const data = overlay.extendData || {};
      const price = Number(data.price || overlay.points?.[0]?.value || 0);
      const width = bounding?.width || 800;
      const isTarget = data.kind === "TARGET";
      const isStop = data.kind === "STOP";
      const isBuy = data.side === "B" || data.side === "BUY";
      const lineColor = isTarget ? "#29b360" : isStop ? "#f05a5a" : isBuy ? "#3b82f6" : "#f97316";
      const bgColor = isTarget ? "#1e7e44" : isStop ? "#aa2e2e" : isBuy ? "#1d4ed8" : "#c2410c";
      const defaultKindLabel = isTarget ? "TARGET" : isStop ? "SL" : "ENTRY";
      const label = data.label || `${defaultKindLabel}: ₹${price.toFixed(2)}`;
      // Every draggable level exposes a grab handle. Persisted levels also get
      // a remove badge; draft (pre-order) levels do not, because removing them
      // before the order exists is just toggling the TP/SL checkbox.
      const showGrip = Boolean(data.draggable);
      const showRemove = showGrip && !data.noRemove;
      const removeX = showRemove ? width - 14 : null;
      const gripX = showGrip ? (showRemove ? width - 34 : width - 14) : null;
      let labelX = width - 8;
      if (showGrip) labelX = gripX - 11;
      if (!showGrip && showRemove) labelX = removeX - 10;
      if (showGrip) {
        data.gripX = gripX;
        data.gripY = y;
      }
      if (showRemove) {
        data.badgeX = removeX;
        data.badgeY = y;
      }

      const figures = [];

      figures.push({
        type: "line",
        attrs: {
          coordinates: [
            { x: 0, y },
            { x: width, y },
          ],
        },
        styles: {
          style: "dashed",
          dashedValue: isTarget ? [4, 3] : isStop ? [2, 2] : [6, 4],
          size: 1.5,
          color: lineColor,
        },
        ignoreEvent: true,
      });

      figures.push({
        type: "text",
        attrs: {
          x: labelX,
          y,
          text: label,
          align: "right",
          baseline: "middle",
        },
        styles: {
          color: "#ffffff",
          backgroundColor: bgColor,
          size: 10,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 6,
          paddingRight: 6,
          paddingTop: 2,
          paddingBottom: 2,
          borderRadius: 3,
        },
        ignoreEvent: true,
      });

      if (showGrip) {
        figures.push({
          type: "text",
          attrs: {
            x: gripX,
            y,
            text: "⋮⋮",
            align: "center",
            baseline: "middle",
          },
          styles: {
            color: "#e2e8f0",
            backgroundColor: "#334155",
            size: 10,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 3,
            paddingRight: 3,
            paddingTop: 2,
            paddingBottom: 2,
            borderRadius: 3,
          },
          ignoreEvent: true,
        });
      }

      if (showRemove) {
        figures.push({
          type: "text",
          attrs: {
            x: removeX,
            y,
            text: "✕",
            align: "center",
            baseline: "middle",
          },
          styles: {
            color: "#ffffff",
            backgroundColor: "#7f1d1d",
            size: 10,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 3,
            paddingRight: 3,
            paddingTop: 1,
            paddingBottom: 1,
            borderRadius: 6,
          },
          ignoreEvent: true,
        });
      }

      return figures;
    },
  });

  kc.registerOverlay({
    name: "dragLevelLine",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.y)) return [];
      const y = coord.y;
      const data = overlay.extendData || {};
      const price = Number(data.price || overlay.points?.[0]?.value || 0);
      const width = bounding?.width || 800;
      const accent = "#f0b429";
      const label = data.label || `MOVE: ₹${price.toFixed(2)}`;

      return [
        {
          type: "line",
          attrs: {
            coordinates: [
              { x: 0, y },
              { x: width, y },
            ],
          },
          styles: {
            style: "dashed",
            dashedValue: [7, 4],
            size: 2.5,
            color: accent,
          },
          ignoreEvent: true,
        },
        {
          type: "text",
          attrs: {
            x: width - 8,
            y,
            text: label,
            align: "right",
            baseline: "middle",
          },
          styles: {
            color: "#000000",
            backgroundColor: accent,
            size: 10,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 6,
            paddingRight: 6,
            paddingTop: 2,
            paddingBottom: 2,
            borderRadius: 3,
          },
          ignoreEvent: true,
        },
      ];
    },
  });

  kc.registerOverlay({
    name: "openOrderLine",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.y)) return [];
      const y = coord.y;
      const data = overlay.extendData || {};
      const isBuy = data.side === "B" || data.side === "BUY";
      const color = isBuy ? "#3b82f6" : "#f97316";
      const width = bounding?.width || 800;

      const figures = [];

      figures.push({
        type: "line",
        attrs: {
          coordinates: [
            { x: 0, y },
            { x: width, y },
          ],
        },
        styles: {
          style: "dashed",
          dashedValue: [5, 3],
          size: 1.5,
          color,
        },
        ignoreEvent: true,
      });

      const sideStr = isBuy ? "BUY" : "SELL";
      const qty = data.qty ? `${data.qty} ` : "";
      const priceStr = Number(data.price || 0).toFixed(2);
      const tagText = `OPEN ${sideStr}: ${qty}@ ₹${priceStr} · EXCH`;

      const showRemove = Boolean(data.draggable);
      const showGrip = Boolean(data.draggable);
      const removeX = showRemove ? width - 14 : null;
      const gripX = showGrip ? (showRemove ? width - 34 : width - 14) : null;
      let labelX = width - 8;
      if (showGrip) labelX = gripX - 11;
      if (showGrip) {
        data.gripX = gripX;
        data.gripY = y;
      }
      if (showRemove) {
        data.badgeX = removeX;
        data.badgeY = y;
      }

      figures.push({
        type: "text",
        attrs: {
          x: labelX,
          y,
          text: tagText,
          align: "right",
          baseline: "middle",
        },
        styles: {
          color: "#ffffff",
          backgroundColor: color,
          size: 10,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 6,
          paddingRight: 6,
          paddingTop: 2,
          paddingBottom: 2,
          borderRadius: 3,
        },
        ignoreEvent: true,
      });

      if (showGrip) {
        figures.push({
          type: "text",
          attrs: {
            x: gripX,
            y,
            text: "⋮⋮",
            align: "center",
            baseline: "middle",
          },
          styles: {
            color: "#e2e8f0",
            backgroundColor: "#334155",
            size: 10,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 3,
            paddingRight: 3,
            paddingTop: 2,
            paddingBottom: 2,
            borderRadius: 3,
          },
          ignoreEvent: true,
        });
      }

      if (showRemove) {
        figures.push({
          type: "text",
          attrs: {
            x: removeX,
            y,
            text: "✕",
            align: "center",
            baseline: "middle",
          },
          styles: {
            color: "#ffffff",
            backgroundColor: "#7f1d1d",
            size: 10,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 3,
            paddingRight: 3,
            paddingTop: 1,
            paddingBottom: 1,
            borderRadius: 6,
          },
          ignoreEvent: true,
        });
      }

      return figures;
    },
  });

  kc.registerOverlay({
    name: "candlestickPatternMarker",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.x) || !Number.isFinite(coord.y)) return [];

      const data = overlay.extendData || {};
      const bias = data.bias || "neutral";
      const isBull = bias === "bullish";
      const isBear = bias === "bearish";

      const bgColor = isBull ? "#166534" : (isBear ? "#991b1b" : "#92400e");
      const arrowColor = isBull ? "#22c55e" : (isBear ? "#ef4444" : "#f59e0b");
      const figures = [];

      if (isBull) {
        figures.push({
          type: "polygon",
          attrs: {
            coordinates: [
              { x: coord.x, y: coord.y + 4 },
              { x: coord.x - 4, y: coord.y + 11 },
              { x: coord.x + 4, y: coord.y + 11 },
            ],
          },
          styles: { style: "fill", color: arrowColor },
          ignoreEvent: true,
        });
      } else if (isBear) {
        figures.push({
          type: "polygon",
          attrs: {
            coordinates: [
              { x: coord.x, y: coord.y - 4 },
              { x: coord.x - 4, y: coord.y - 11 },
              { x: coord.x + 4, y: coord.y - 11 },
            ],
          },
          styles: { style: "fill", color: arrowColor },
          ignoreEvent: true,
        });
      }

      const textY = isBull ? coord.y + 13 : coord.y - 13;
      const baseline = isBull ? "top" : "bottom";
      const label = data.label || data.name || "PATT";

      figures.push({
        type: "text",
        attrs: {
          x: coord.x,
          y: textY,
          text: label,
          align: "center",
          baseline,
        },
        styles: {
          color: "#ffffff",
          backgroundColor: bgColor,
          size: 9,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 4,
          paddingRight: 4,
          paddingTop: 1.5,
          paddingBottom: 1.5,
          borderRadius: 3,
        },
        ignoreEvent: true,
      });

      return figures;
    },
  });

  kc.registerOverlay({
    name: "geometricPatternOverlay",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates }) {
      if (!coordinates || coordinates.length < 2) return [];
      const data = overlay.extendData || {};
      const bias = data.bias || "neutral";
      const isBull = bias === "bullish";
      const isBear = bias === "bearish";
      const accentColor = isBull ? "#22c55e" : (isBear ? "#ef4444" : "#38bdf8");
      const figures = [];

      if (data.resistance && data.support && coordinates.length >= 4) {
        const r1 = coordinates[0];
        const r2 = coordinates[1];
        const s1 = coordinates[2];
        const s2 = coordinates[3];

        if (r1 && r2 && s1 && s2) {
          figures.push({
            type: "polygon",
            attrs: {
              coordinates: [
                { x: r1.x, y: r1.y },
                { x: r2.x, y: r2.y },
                { x: s2.x, y: s2.y },
                { x: s1.x, y: s1.y },
              ],
            },
            styles: {
              style: "fill",
              color: isBull ? "rgba(34, 197, 94, 0.08)" : (isBear ? "rgba(239, 68, 68, 0.08)" : "rgba(56, 189, 248, 0.08)"),
            },
            ignoreEvent: true,
          });

          figures.push({
            type: "line",
            attrs: { coordinates: [{ x: r1.x, y: r1.y }, { x: r2.x, y: r2.y }] },
            styles: { style: "dashed", dashedValue: [6, 3], size: 1.6, color: "#f05a5a" },
            ignoreEvent: true,
          });

          figures.push({
            type: "line",
            attrs: { coordinates: [{ x: s1.x, y: s1.y }, { x: s2.x, y: s2.y }] },
            styles: { style: "dashed", dashedValue: [6, 3], size: 1.6, color: "#29b360" },
            ignoreEvent: true,
          });

          const maxX = Math.max(r2.x, s2.x);
          const midY = (r2.y + s2.y) / 2;
          figures.push({
            type: "text",
            attrs: {
              x: maxX + 6,
              y: midY,
              text: data.label || data.name,
              align: "left",
              baseline: "middle",
            },
            styles: {
              color: "#ffffff",
              backgroundColor: isBull ? "#166534" : (isBear ? "#991b1b" : "#0284c7"),
              size: 9.5,
              weight: "bold",
              family: "system-ui, -apple-system, sans-serif",
              paddingLeft: 5,
              paddingRight: 5,
              paddingTop: 2,
              paddingBottom: 2,
              borderRadius: 3,
            },
            ignoreEvent: true,
          });

          return figures;
        }
      }

      const lineCoords = coordinates.filter(c => c && Number.isFinite(c.x) && Number.isFinite(c.y));
      if (lineCoords.length >= 2) {
        figures.push({
          type: "line",
          attrs: { coordinates: lineCoords.map(c => ({ x: c.x, y: c.y })) },
          styles: { style: "solid", size: 1.6, color: accentColor },
          ignoreEvent: true,
        });

        for (let i = 0; i < lineCoords.length; i++) {
          const pt = lineCoords[i];
          const ptData = data.points?.[i] || {};
          figures.push({
            type: "circle",
            attrs: { x: pt.x, y: pt.y, r: 3 },
            styles: { style: "fill", color: accentColor, borderColor: "#ffffff", borderSize: 1 },
            ignoreEvent: true,
          });

          if (ptData.label) {
            figures.push({
              type: "text",
              attrs: {
                x: pt.x,
                y: pt.y - 7,
                text: ptData.label,
                align: "center",
                baseline: "bottom",
              },
              styles: {
                color: "#e2e8f0",
                backgroundColor: "rgba(15, 23, 42, 0.8)",
                size: 8.5,
                weight: "bold",
                family: "system-ui, -apple-system, sans-serif",
                paddingLeft: 3,
                paddingRight: 3,
                paddingTop: 1,
                paddingBottom: 1,
                borderRadius: 2,
              },
              ignoreEvent: true,
            });
          }
        }

        const lastPt = lineCoords[lineCoords.length - 1];
        const targetStr = data.targetPrice ? ` 🎯 ₹${Number(data.targetPrice).toFixed(1)}` : "";
        figures.push({
          type: "text",
          attrs: {
            x: lastPt.x + 8,
            y: lastPt.y,
            text: `${data.label}${targetStr}`,
            align: "left",
            baseline: "middle",
          },
          styles: {
            color: "#ffffff",
            backgroundColor: isBull ? "#166534" : (isBear ? "#991b1b" : "#0284c7"),
            size: 9.5,
            weight: "bold",
            family: "system-ui, -apple-system, sans-serif",
            paddingLeft: 5,
            paddingRight: 5,
            paddingTop: 2,
            paddingBottom: 2,
            borderRadius: 3,
          },
          ignoreEvent: true,
        });
      }

      return figures;
    },
  });

  kc.registerOverlay({
    name: "harmonicPatternOverlay",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates }) {
      if (!coordinates || coordinates.length < 4) return [];
      const data = overlay.extendData || {};
      const isBull = data.bias === "bullish";
      const lineColor = isBull ? "#22c55e" : "#ef4444";
      const fillXAB = isBull ? "rgba(34, 197, 94, 0.14)" : "rgba(239, 68, 68, 0.14)";
      const fillBCD = isBull ? "rgba(34, 197, 94, 0.20)" : "rgba(239, 68, 68, 0.20)";
      const figures = [];

      if (coordinates.length >= 5) {
        const X = coordinates[0];
        const A = coordinates[1];
        const B = coordinates[2];
        const C = coordinates[3];
        const D = coordinates[4];

        if (X && A && B && C && D) {
          figures.push({
            type: "polygon",
            attrs: { coordinates: [{ x: X.x, y: X.y }, { x: A.x, y: A.y }, { x: B.x, y: B.y }] },
            styles: { style: "fill", color: fillXAB },
            ignoreEvent: true,
          });

          figures.push({
            type: "polygon",
            attrs: { coordinates: [{ x: B.x, y: B.y }, { x: C.x, y: C.y }, { x: D.x, y: D.y }] },
            styles: { style: "fill", color: fillBCD },
            ignoreEvent: true,
          });

          figures.push({
            type: "line",
            attrs: {
              coordinates: [
                { x: X.x, y: X.y },
                { x: A.x, y: A.y },
                { x: B.x, y: B.y },
                { x: C.x, y: C.y },
                { x: D.x, y: D.y },
              ],
            },
            styles: { style: "solid", size: 1.5, color: lineColor },
            ignoreEvent: true,
          });

          figures.push({
            type: "line",
            attrs: { coordinates: [{ x: X.x, y: X.y }, { x: B.x, y: B.y }] },
            styles: { style: "dashed", dashedValue: [3, 3], size: 1, color: "rgba(255,255,255,0.4)" },
            ignoreEvent: true,
          });
          figures.push({
            type: "line",
            attrs: { coordinates: [{ x: A.x, y: A.y }, { x: C.x, y: C.y }] },
            styles: { style: "dashed", dashedValue: [3, 3], size: 1, color: "rgba(255,255,255,0.4)" },
            ignoreEvent: true,
          });
          figures.push({
            type: "line",
            attrs: { coordinates: [{ x: X.x, y: X.y }, { x: D.x, y: D.y }] },
            styles: { style: "dashed", dashedValue: [3, 3], size: 1, color: "rgba(255,255,255,0.4)" },
            ignoreEvent: true,
          });

          const labels = ["X", "A", "B", "C", "D"];
          const pts = [X, A, B, C, D];
          for (let i = 0; i < 5; i++) {
            figures.push({
              type: "circle",
              attrs: { x: pts[i].x, y: pts[i].y, r: 3.5 },
              styles: { style: "fill", color: lineColor, borderColor: "#ffffff", borderSize: 1 },
              ignoreEvent: true,
            });
            figures.push({
              type: "text",
              attrs: {
                x: pts[i].x,
                y: pts[i].y - 8,
                text: labels[i],
                align: "center",
                baseline: "bottom",
              },
              styles: {
                color: "#ffffff",
                backgroundColor: "rgba(15, 23, 42, 0.85)",
                size: 9,
                weight: "bold",
                paddingLeft: 3,
                paddingRight: 3,
                paddingTop: 1,
                paddingBottom: 1,
                borderRadius: 2,
              },
              ignoreEvent: true,
            });
          }

          if (data.ratios?.xb) {
            figures.push({
              type: "text",
              attrs: {
                x: (X.x + B.x) / 2,
                y: (X.y + B.y) / 2,
                text: `${data.ratios.xb}`,
                align: "center",
                baseline: "middle",
              },
              styles: {
                color: "#fbbf24",
                backgroundColor: "rgba(15, 23, 42, 0.85)",
                size: 8,
                weight: "bold",
                paddingLeft: 2,
                paddingRight: 2,
              },
              ignoreEvent: true,
            });
          }

          const przText = data.prz ? `PRZ: ₹${data.prz.low.toFixed(1)} - ₹${data.prz.high.toFixed(1)}` : "PRZ";
          figures.push({
            type: "text",
            attrs: {
              x: D.x + 8,
              y: D.y,
              text: `🎯 ${data.name.toUpperCase()} [${przText}]`,
              align: "left",
              baseline: "middle",
            },
            styles: {
              color: "#ffffff",
              backgroundColor: isBull ? "#15803d" : "#b91c1c",
              size: 9.5,
              weight: "bold",
              family: "system-ui, -apple-system, sans-serif",
              paddingLeft: 5,
              paddingRight: 5,
              paddingTop: 2,
              paddingBottom: 2,
              borderRadius: 3,
            },
            ignoreEvent: true,
          });
        }
      } else if (coordinates.length === 4) {
        const A = coordinates[0];
        const B = coordinates[1];
        const C = coordinates[2];
        const D = coordinates[3];
        if (A && B && C && D) {
          figures.push({
            type: "line",
            attrs: {
              coordinates: [
                { x: A.x, y: A.y },
                { x: B.x, y: B.y },
                { x: C.x, y: C.y },
                { x: D.x, y: D.y },
              ],
            },
            styles: { style: "solid", size: 1.5, color: lineColor },
            ignoreEvent: true,
          });
          figures.push({
            type: "text",
            attrs: {
              x: D.x + 8,
              y: D.y,
              text: `🎯 ${data.name}`,
              align: "left",
              baseline: "middle",
            },
            styles: {
              color: "#ffffff",
              backgroundColor: isBull ? "#15803d" : "#b91c1c",
              size: 9.5,
              weight: "bold",
              paddingLeft: 5,
              paddingRight: 5,
              paddingTop: 2,
              paddingBottom: 2,
              borderRadius: 3,
            },
            ignoreEvent: true,
          });
        }
      }

      return figures;
    },
  });

  kc.registerOverlay({
    name: "liquidityPatternOverlay",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.y)) return [];

      const data = overlay.extendData || {};
      const isBull = data.bias === "bullish";
      const color = isBull ? "#22c55e" : "#ef4444";
      const width = bounding?.width || 800;
      const figures = [];

      figures.push({
        type: "line",
        attrs: {
          coordinates: [
            { x: Math.max(0, coord.x - 70), y: coord.y },
            { x: Math.min(width, coord.x + 70), y: coord.y },
          ],
        },
        styles: {
          style: "dashed",
          dashedValue: [4, 3],
          size: 1.5,
          color,
        },
        ignoreEvent: true,
      });

      const label = data.label || data.name || "LIQUIDITY SETUP";
      figures.push({
        type: "text",
        attrs: {
          x: coord.x,
          y: isBull ? coord.y + 14 : coord.y - 14,
          text: label,
          align: "center",
          baseline: isBull ? "top" : "bottom",
        },
        styles: {
          color: "#ffffff",
          backgroundColor: isBull ? "#14532d" : "#7f1d1d",
          size: 9,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 5,
          paddingRight: 5,
          paddingTop: 1.5,
          paddingBottom: 1.5,
          borderRadius: 3,
        },
        ignoreEvent: true,
      });

      return figures;
    },
  });

  kc.registerOverlay({
    name: "signalMarker",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || !coordinates.length) return [];
      const coord = coordinates[0];
      if (!coord || !Number.isFinite(coord.x) || !Number.isFinite(coord.y)) return [];
      const data = overlay.extendData || {};
      const dir = data.dir === "down" ? "down" : "up";
      const up = dir === "up";
      const color = data.color || (up ? "#29b360" : "#f05a5a");
      const offset = Number.isFinite(data.offset) ? data.offset : 10;
      const size = Number.isFinite(data.size) ? data.size : 4;
      const tipY = up ? coord.y + offset : coord.y - offset;
      const baseY = up ? tipY + size * 2 : tipY - size * 2;

      const figures: any[] = [
        {
          type: "polygon",
          attrs: {
            coordinates: [
              { x: coord.x, y: tipY },
              { x: coord.x - size, y: baseY },
              { x: coord.x + size, y: baseY },
            ],
          },
          styles: { style: "fill", color },
          ignoreEvent: true,
        },
      ];

      if (data.label) {
        const labelStyles = {
          color: "#ffffff",
          backgroundColor: color,
          size: 9,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 4,
          paddingRight: 4,
          paddingTop: 1,
          paddingBottom: 1,
          borderRadius: 2,
        };
        figures.push({
          type: "text",
          attrs: clampTextFigureX({
            x: coord.x,
            y: up ? baseY + 2 : baseY - 2,
            text: String(data.label),
            align: "center",
            baseline: up ? "top" : "bottom",
          }, labelStyles, bounding?.width),
          styles: labelStyles,
          ignoreEvent: true,
        });
      }

      return figures;
    },
  });

  kc.registerOverlay({
    name: "divergenceMarker",
    totalStep: 1,
    needDefaultPointFigure: false,
    needDefaultXAxisFigure: false,
    needDefaultYAxisFigure: false,
    createPointFigures: function({ overlay, coordinates, bounding }) {
      if (!coordinates || coordinates.length < 2) return [];
      const [a, b] = coordinates;
      if (!a || !b || !Number.isFinite(a.x) || !Number.isFinite(b.x)) return [];
      const data = overlay.extendData || {};
      const isBull = data.kind === "bull";
      const color = isBull ? "#29b360" : "#f05a5a";

      const figures: any[] = [
        {
          type: "line",
          attrs: {
            coordinates: [
              { x: a.x, y: a.y },
              { x: b.x, y: b.y },
            ],
          },
          styles: { style: "solid", size: 1.5, color },
          ignoreEvent: true,
        },
      ];

      for (const point of [a, b]) {
        figures.push({
          type: "circle",
          attrs: { x: point.x, y: point.y, r: 3 },
          styles: { style: "fill", color },
          ignoreEvent: true,
        });
      }

      figures.push({
        type: "text",
        attrs: clampTextFigureX({
          x: b.x,
          y: isBull ? b.y + 16 : b.y - 16,
          text: isBull ? "BULL DIV" : "BEAR DIV",
          align: "center",
          baseline: isBull ? "top" : "bottom",
        }, { size: 9, weight: "bold", paddingLeft: 4, paddingRight: 4, borderSize: 0 }, bounding?.width),
        styles: {
          color: "#ffffff",
          backgroundColor: isBull ? "#14532d" : "#7f1d1d",
          size: 9,
          weight: "bold",
          family: "system-ui, -apple-system, sans-serif",
          paddingLeft: 5,
          paddingRight: 5,
          paddingTop: 1.5,
          paddingBottom: 1.5,
          borderRadius: 3,
        },
        ignoreEvent: true,
      });

      return figures;
    },
  });
}

function registerCustomIndicators() {
  const kc = window.klinecharts;
  if (!kc || !kc.registerIndicator) return;

  function createVolumeFigure() {
    return {
      key: "volume",
      title: null, // Omit from top header legend so it never moves below into a second row
      type: "bar",
      baseValue: 0,
      styles: function(data) {
        const current = data?.data?.current;
        const defaultStyles = data?.defaultStyles;
        const indicator = data?.indicator;
        const fallbackColor = defaultStyles?.bars?.[0]?.noChangeColor || "#8292a2";
        let color = fallbackColor;
        if (current) {
          if (current.close > current.open) {
            color = indicator?.styles?.bars?.[0]?.upColor || defaultStyles?.bars?.[0]?.upColor || "#35c46a80";
          } else if (current.close < current.open) {
            color = indicator?.styles?.bars?.[0]?.downColor || defaultStyles?.bars?.[0]?.downColor || "#ff5d5d80";
          }
        }
        return { color };
      },
    };
  }

  kc.registerIndicator({
    name: "VOL",
    shortName: "VOL",
    series: "volume",
    calcParams: [5, 10, 20],
    shouldFormatBigNumber: true,
    precision: 0,
    minValue: 0,
    figures: [
      { key: "ma1", title: "MA5: ", type: "line" },
      { key: "ma2", title: "MA10: ", type: "line" },
      { key: "ma3", title: "MA20: ", type: "line" },
      createVolumeFigure(),
    ],
    regenerateFigures: function(calcParams) {
      const figs = (calcParams || []).map((p, idx) => ({
        key: `ma${idx + 1}`,
        title: `MA${p}: `,
        type: "line",
      }));
      figs.push(createVolumeFigure());
      return figs;
    },
    calc: function(dataList, indicator) {
      const params = indicator.calcParams || [];
      const sums = [];
      return (dataList || []).map((candle, idx) => {
        const vol = Number.isFinite(candle?.volume) ? candle.volume : 0;
        const result = {
          volume: vol,
          open: candle?.open,
          close: candle?.close,
        };
        params.forEach((period, pIdx) => {
          sums[pIdx] = (sums[pIdx] || 0) + vol;
          if (idx >= period - 1) {
            result[`ma${pIdx + 1}`] = sums[pIdx] / period;
            const prevBar = dataList[idx - (period - 1)];
            const oldVol = prevBar?.volume;
            sums[pIdx] -= Number.isFinite(oldVol) ? oldVol : 0;
          } else {
            result[`ma${pIdx + 1}`] = null;
          }
        });
        return result;
      });
    },
  });

  kc.registerIndicator({
    name: "TD",
    shortName: "TD",
    calcParams: [],
    precision: 0,
    shouldFormatBigNumber: true,
    figures: [
      { key: "ce", title: "CE decay: ", type: "line", styles: () => ({ color: "#4c8dff", size: 1.5 }) },
      { key: "pe", title: "PE decay: ", type: "line", styles: () => ({ color: "#ff9f43", size: 1.5 }) },
      { key: "zero", title: null, type: "line", styles: () => ({ color: "rgba(130,146,162,0.35)", size: 1 }) },
    ],
    calc: function(dataList) {
      return (dataList || []).map((candle) => ({
        ce: Number.isFinite(candle?.tdCE) ? candle.tdCE : null,
        pe: Number.isFinite(candle?.tdPE) ? candle.tdPE : null,
        zero: 0,
      }));
    },
  });

  kc.registerIndicator({
    name: "RSI14",
    shortName: "RSI",
    calcParams: [14],
    precision: 2,
    minValue: 0,
    maxValue: 100,
    figures: [
      { key: "rsi", title: "RSI: ", type: "line" },
      { key: "overbought", title: null, type: "line", styles: () => ({ color: "rgba(240,90,90,0.45)", size: 1 }) },
      { key: "mid", title: null, type: "line", styles: () => ({ color: "rgba(208,215,222,0.5)", size: 1 }) },
      { key: "oversold", title: null, type: "line", styles: () => ({ color: "rgba(41,179,96,0.45)", size: 1 }) },
    ],
    calc: function(dataList, indicator) {
      const period = Number(indicator?.calcParams?.[0]) || 14;
      const candles = (dataList || []).map((candle) => ({
        time: candle?.timestamp,
        open: candle?.open,
        high: candle?.high,
        low: candle?.low,
        close: candle?.close,
      }));
      const series = Indicators.rsiSeries(candles, period);
      return series.map((value) => ({
        rsi: Number.isFinite(value) ? value : null,
        overbought: 70,
        mid: 50,
        oversold: 30,
      }));
    },
  });

  kc.registerIndicator({
    name: "VWAP",
    shortName: "",
    calcParams: [],
    precision: 2,
    styles: { lines: [{ color: VWAP_COLOR, size: 1.5 }] },
    figures: [
      { key: "vwap", title: "VWAP: ", type: "line", styles: () => ({ color: VWAP_COLOR, size: 1.5 }) },
    ],
    calc: function(dataList) {
      const list = dataList || [];
      const candles = list.map((candle) => ({
        time: candle?.timestamp,
        high: candle?.high,
        low: candle?.low,
        close: candle?.close,
        volume: candle?.volume,
      }));
      // Fallback: VWAP of the displayed series. Only exact for 1m charts; the
      // terminal attaches `vwap` (projected from the 1m base) whenever it has
      // one, which is timeframe-independent and always preferred.
      const fallback = Indicators.vwapSeries(candles, istSessionKey);
      return list.map((candle, index) => {
        const projected = Number(candle?.vwap);
        const value = Number.isFinite(projected) && projected > 0 ? projected : fallback[index];
        return { vwap: Number.isFinite(value) ? value : null };
      });
    },
  });

  // Intraday index-option staples that klinecharts v10 does not ship.
  kc.registerIndicator({
    name: "ATR",
    shortName: "",
    calcParams: [14],
    precision: 2,
    figures: [
      { key: "atr", title: "ATR: ", type: "line", styles: () => ({ color: "#38bdf8", size: 1.4 }) },
    ],
    calc: function(dataList, indicator) {
      const period = Number(indicator?.calcParams?.[0]) || 14;
      const series = atrSeries(dataList || [], period);
      return series.map((value) => ({ atr: Number.isFinite(value) ? value : null }));
    },
  });

  kc.registerIndicator({
    name: "SUPERTREND",
    shortName: "ST",
    calcParams: [10, 3],
    precision: 2,
    figures: [
      { key: "up", title: "ST▲: ", type: "line", styles: () => ({ color: "#29b360", size: 1.5 }) },
      { key: "down", title: "ST▼: ", type: "line", styles: () => ({ color: "#f05a5a", size: 1.5 }) },
    ],
    calc: function(dataList, indicator) {
      const period = Number(indicator?.calcParams?.[0]) || 10;
      const multiplier = Number(indicator?.calcParams?.[1]) || 3;
      const series = supertrendSeries(dataList || [], period, multiplier);
      return (dataList || []).map((_, index) => ({
        up: series.up[index] ?? null,
        down: series.down[index] ?? null,
      }));
    },
  });

  // Per-bar open interest stored with the candle (CE/PE panes only: the cash
  // index has no OI). Level and change are separate panes so the histogram is
  // not flattened by the much larger absolute OI scale.
  kc.registerIndicator({
    name: "OI",
    shortName: "OI",
    calcParams: [],
    precision: 0,
    shouldFormatBigNumber: true,
    minValue: 0,
    figures: [
      { key: "oi", title: "OI: ", type: "line", styles: () => ({ color: "#f0b429", size: 1.5 }) },
    ],
    calc: function(dataList) {
      return (dataList || []).map((candle) => {
        const oi = Number(candle?.oi);
        return { oi: Number.isFinite(oi) && oi > 0 ? oi : null };
      });
    },
  });

  kc.registerIndicator({
    name: "OIDELTA",
    shortName: "ΔOI",
    calcParams: [],
    precision: 0,
    shouldFormatBigNumber: true,
    figures: [
      {
        key: "doi",
        title: "ΔOI/bar: ",
        type: "bar",
        baseValue: 0,
        styles: function(data) {
          const value = Number(data?.data?.current?.doi);
          return { color: value < 0 ? "#f05a5a99" : "#29b36099" };
        },
      },
    ],
    calc: function(dataList) {
      const deltas = oiDeltaSeries(dataList || []);
      return (dataList || []).map((_, index) => ({
        doi: Number.isFinite(deltas[index]) ? deltas[index] : null,
      }));
    },
  });
}

/**
 * VWAP series for exports. Prefers the session VWAP the terminal projected
 * from the 1-minute base (attached as `vwap`) so an exported report never
 * disagrees with the line on screen; falls back to the series-local formula
 * when the projection is unavailable.
 */
function exportVwapSeries(data) {
  const bars = Array.isArray(data) ? data : [];
  const fallback = Indicators.vwapSeries(bars, istSessionKey);
  return bars.map((candle, index) => {
    const projected = Number(candle?.vwap);
    return Number.isFinite(projected) && projected > 0 ? projected : fallback[index];
  });
}

function patchIndicatorLastValueView(chart) {
  try {
    const yAxisWidget = chart?._drawPanes?.[0]?._yAxisWidgets?.values()?.next()?.value;
    const view = yAxisWidget?._indicatorLastValueView;
    if (!view) return;
    const proto = Object.getPrototypeOf(view);
    if (!proto || proto._scalperPatched) return;
    proto._scalperPatched = true;

    proto.drawImp = function(ctx) {
      const widget = this.getWidget();
      const pane = widget.getPane();
      const bounding = widget.getBounding();
      const chartStore = pane.getChart().getChartStore();
      const styles = chartStore.getStyles().indicator;
      const lastValueMark = styles.lastValueMark;
      const textStyle = lastValueMark?.text;

      if (!lastValueMark?.show) return;

      const axis = widget.getAxisComponent();
      const range = axis.getRange();
      const dataList = chartStore.getDataList();
      const lastIdx = dataList.length - 1;
      if (lastIdx < 0) return;

      const yAxisIds = new Set([axis.id]);
      const defaultYAxisId = pane.getDefaultYAxisId();
      if (pane.isManualYAxis(axis.id) && defaultYAxisId != null) {
        yAxisIds.add(defaultYAxisId);
      }

      const indicators = chartStore.getIndicatorsByPaneId(pane.getId()).filter((ind) => yAxisIds.has(ind.yAxisId));
      const formatter = chartStore.getInnerFormatter();
      const decimalFold = chartStore.getDecimalFold();
      const thousands = chartStore.getThousandsSeparator();

      // Collect first: badges that land within a few pixels of each other (VWAP
      // next to the last price, two MAs on the same tick) are stacked apart
      // instead of painted on top of each other.
      const badges = [];
      indicators.forEach((ind) => {
        const lastResult = ind.result?.[lastIdx];
        if (!lastResult || !ind.visible) return;

        const figures = ind.figures || [];
        figures.forEach((fig, figIdx) => {
          // If VOL indicator, ONLY draw lastValueMark for live volume on the axis, skip MAs
          if (ind.name === "VOL" && fig.key !== "volume") return;

          const val = lastResult[fig.key];
          if (!Number.isFinite(val)) return;

          const y = axis.convertToNicePixel(val);
          let text = axis.displayValueToText(
            axis.realValueToDisplayValue(axis.valueToRealValue(val, { range }), { range }),
            ind.precision
          );
          if (ind.shouldFormatBigNumber && formatter) {
            text = formatter.formatBigNumber(text);
          }
          if (decimalFold && thousands) {
            text = decimalFold.format(thousands.format(text));
          }

          const lastBar = dataList[lastIdx];
          const isUp = lastBar && lastBar.close >= lastBar.open;
          let bgColor = fig.key === "volume"
            ? (isUp ? (styles.bars?.[0]?.upColor || "#35c46a") : (styles.bars?.[0]?.downColor || "#ff5d5d"))
            : (ind.styles?.lines?.[figIdx]?.color || styles.lines?.[figIdx]?.color || "#2196f3");

          // Solid color on the axis badge
          if (typeof bgColor === "string" && bgColor.startsWith("#") && bgColor.length === 9) {
            bgColor = bgColor.slice(0, 7);
          }

          badges.push({ y, text, bgColor });
        });
      });
      if (!badges.length) return;

      const size = Number(textStyle?.size) || 11;
      const paddingTop = Number(textStyle?.paddingTop) || 0;
      const paddingBottom = Number(textStyle?.paddingBottom) || 0;
      const halfHeight = (size + paddingTop + paddingBottom) / 2 + 1;
      const paneHeight = Number(bounding.height) || 0;
      if (!(paneHeight > 0)) return;
      const maxY = Math.max(halfHeight, paneHeight - halfHeight);

      // The library's last-price tag is painted before this view, so reserve
      // its slot and push our badges around it.
      const occupied = [];
      const candleStyles = chartStore.getStyles().candle;
      const lastMark = candleStyles?.priceMark;
      if (pane.getId() === "candle_pane" && lastMark?.show && lastMark.last?.show && lastMark.last.text?.show) {
        const close = Number(dataList[lastIdx]?.close);
        if (Number.isFinite(close)) {
          const priceY = axis.convertToNicePixel(close);
          // The tag height is size + both paddings; the `|| 12` only replaces a
          // missing size, it must not swallow the paddings too.
          const priceSize = Number(lastMark.last.text.size) || 12;
          const priceHalf = (priceSize
            + (Number(lastMark.last.text.paddingTop) || 0)
            + (Number(lastMark.last.text.paddingBottom) || 0)) / 2 + 1;
          if (Number.isFinite(priceY)) occupied.push({ top: priceY - priceHalf, bottom: priceY + priceHalf });
        }
      }

      const isFromZero = typeof axis.isFromZero === "function" ? axis.isFromZero() : false;
      const x = isFromZero ? RIGHT_AXIS_GUTTER : Math.max(0, (Number(bounding.width) || 0) - RIGHT_AXIS_GUTTER);
      const align = isFromZero ? "left" : "right";

      badges.sort((a, b) => a.y - b.y);

      // A short pane (or one already taken by the last-price tag) can only hold
      // a few badges. Anything beyond that would be clamped onto its neighbour,
      // so collapse the overflow into one compact "+N" tag instead.
      const slotHeight = 2 * halfHeight + TAG_STACK_GAP;
      const usableRange = Math.max(0, paneHeight - 2 * halfHeight);
      const reserved = occupied.reduce((sum, rect) => sum + (rect.bottom - rect.top) + TAG_STACK_GAP, 0);
      const capacity = Math.max(1, Math.floor(Math.max(0, usableRange - reserved) / slotHeight) + 1);
      let shown = badges;
      if (badges.length > capacity) {
        const keep = Math.max(0, capacity - 1);
        const hidden = badges.length - keep;
        shown = badges.slice(0, keep);
        shown.push({
          y: badges[keep]?.y ?? (shown[shown.length - 1]?.y ?? halfHeight) + slotHeight,
          text: `+${hidden}`,
          bgColor: "#64748b",
        });
      }

      const collides = (candidate) => occupied.some(
        (rect) => candidate + halfHeight > rect.top && candidate - halfHeight < rect.bottom
      );
      const scanStep = Math.max(1, halfHeight);
      for (const badge of shown) {
        const desired = Math.min(maxY, Math.max(halfHeight, badge.y));
        let y = null;
        if (!collides(desired)) {
          y = desired;
        } else {
          // Walk outwards in half-tag steps: any free gap wide enough for one
          // tag contains a grid point, so this finds it without a pixel scan.
          for (let distance = scanStep; distance <= paneHeight + scanStep; distance += scanStep) {
            const below = desired + distance;
            const above = desired - distance;
            if (below <= maxY && !collides(below)) {
              y = below;
              break;
            }
            if (above >= halfHeight && !collides(above)) {
              y = above;
              break;
            }
            if (below > maxY && above < halfHeight) break;
          }
        }
        // A pane shorter than one tag + the last-price tag has no free slot:
        // still paint the single tag (over the last-price pill) rather than
        // dropping the only remaining read-out.
        if (y == null) y = desired;
        occupied.push({ top: y - halfHeight, bottom: y + halfHeight });

        const figure = this.createFigure({
          name: "text",
          attrs: { x, y, text: badge.text, align, baseline: "middle" },
          styles: Object.assign({}, textStyle, { backgroundColor: badge.bgColor }),
        });
        if (figure) figure.draw(ctx);
      }
    };
  } catch (err) {
    console.warn("patchIndicatorLastValueView error:", err);
  }
}

export class CandleChart {
  [key: string]: any;
  constructor(container, { showVolume = true, showSR = false, showTrades = true, showAutoTrendlines = false, showPatterns = false, showFib = false, showMA = false, showEMA = undefined, showVWAP = false, showRSI = false, showDivergence = false, showMomentum = false, showSupertrend = false, showATR = false, showOIPane = false, showOIDeltaPane = false, showOIWalls = false, showTheta = false, showMaxPain = false, showVolumeProfile = false, showDepth = true, onLevelDrop = null, onLevelRemove = null, onLevelDragStart = null, onLevelDragEnd = null, onVolumeProfile = null } = {}) {
    this.container = container;
    this.data = [];
    this.showVolume = showVolume;
    this.showSR = showSR;
    this.showTrades = showTrades;
    this.showAutoTrendlines = showAutoTrendlines;
    this.showPatterns = showPatterns;
    this.showFib = Boolean(showFib);
    this.showEMA = Boolean(showEMA ?? showMA);
    this.showVWAP = Boolean(showVWAP);
    this.showRSI = Boolean(showRSI);
    this.showDivergence = Boolean(showDivergence);
    this.showMomentum = Boolean(showMomentum);
    this.showSupertrend = Boolean(showSupertrend);
    this.showATR = Boolean(showATR);
    this.showOIPane = Boolean(showOIPane);
    this.showOIDeltaPane = Boolean(showOIDeltaPane);
    this.showOIWalls = Boolean(showOIWalls);
    this.oiWalls = [];
    this.oiWallOverlayIds = [];
    this.showTheta = Boolean(showTheta);
    this.showMaxPain = Boolean(showMaxPain);
    this.maxPainValue = null;
    this.maxPainMeta = null;
    this.showVolumeProfile = Boolean(showVolumeProfile);
    this.vpOverlayId = null;
    this.vpLastKey = "";
    this.vpSuggestion = null;
    this._vpTimer = null;
    this._vpLastRefresh = 0;
    this.onVolumeProfile = typeof onVolumeProfile === "function" ? onVolumeProfile : null;
    this.thetaMeta = null;
    this.thetaDataReady = false;
    this.showDepth = Boolean(showDepth);
    this.emaCrossOverlayIds = [];
    this.divergenceOverlayIds = [];
    this.momentumOverlayIds = [];
    this.indicatorSignature = "";
    this.depthOverlayIds = [];
    this.depthData = null;
    this.chainData = [];
    this._depthTimer = null;
    this.patternConfig = { candlestick: true, geometric: true, harmonic: true, liquidity: true };
    this.srOverlayIds = [];
    this.autoTrendlineOverlayIds = [];
    this.patternOverlayIds = [];
    this.signalOverlayIds = [];
    this.showSignals = false;
    this.fibOverlayIds = [];
    this.tradeOverlayIds = [];
    this.positionOverlayId = null;
    this.bracketLevelOverlayIds = [];
    this.bracketLevelsKey = "";
    this.bracketData = null;
    this.dayDividerOverlayIds = [];
    this.dayDividerKey = "";
    this.openOrderOverlayIds = [];
    this.openOrdersData = [];
    this.tradesData = [];
    this.positionData = null;
    this.isSpotChart = false;
    this.drawingTrendlineId = null;
    this.drawingTrendlineBtn = null;
    this.drawingEscHandler = null;
    this.onLevelDrop = typeof onLevelDrop === "function" ? onLevelDrop : null;
    this.onLevelRemove = typeof onLevelRemove === "function" ? onLevelRemove : null;
    this.onLevelDragStart = typeof onLevelDragStart === "function" ? onLevelDragStart : null;
    this.onLevelDragEnd = typeof onLevelDragEnd === "function" ? onLevelDragEnd : null;
    this.draggableOrderLevels = [];
    this.draggableBracketLevels = [];
    // Draft levels belong to the unsubmitted quick-order card: they are only
    // local prices until the user presses BUY/SELL, but they drag exactly like
    // a placed bracket level so TP/SL can be chosen directly on the chart.
    this.draftLevels = { entry: 0, target: 0, stop: 0, side: "B" };
    this.draftLevelsKey = "";
    this.draftLevelOverlayIds = [];
    this.levelDrag = null;
    this.levelDragPreviewId = null;
    this.suppressChartClickUntil = 0;
    this.levelPointerHandlers = null;
    this.colors = chartColors(container);
    this.loaderCallback = null;
    this.subscriberCallback = null;
    this.compactPane = null;
    this.pixelRatioQuery = null;
    // Natural (unstressed) height of every indicator pane, plus the height the
    // pane sizing pass last applied, so sizes can be restored when room returns.
    this.paneSizing = new Map();
    this.canvasRatio = canvasPixelRatio();

    const kc = window.klinecharts;
    if (!kc) {
      console.error("klinecharts library not available on window.klinecharts");
      return;
    }

    registerCustomOverlays();
    registerCustomIndicators();

    this.chart = kc.init(container, {
      styles: buildStyles(this.colors),
    });

    if (!this.chart) return;

    patchIndicatorLastValueView(this.chart);
    patchCanvasPixelRatio(this.chart);
    patchYAxisRightGutter(this.chart);
    patchXAxisTickLabels(this.chart);
    this.syncChartDensity();
    this.watchPixelRatio();

    const origRemoveOverlay = this.chart.removeOverlay.bind(this.chart);
    this.chart.removeOverlay = (filter) => {
      if (typeof filter === "string") {
        return origRemoveOverlay({ id: filter });
      }
      return origRemoveOverlay(filter);
    };

    const origCreateIndicator = this.chart.createIndicator.bind(this.chart);
    this.chart.createIndicator = (value, isStack, paneOptions, callback) => {
      let indicator = typeof value === "string" ? { name: value } : Object.assign({}, value);
      if ((indicator.name === "MA" || indicator.name === "EMA" || indicator.name === "RSI14") && paneOptions && paneOptions.id && !indicator.paneId) {
        indicator.paneId = paneOptions.id;
      }
      const result = origCreateIndicator(indicator, isStack, paneOptions, callback);
      // A new indicator pane consumes container height; re-budget the stack so
      // the candle pane keeps its minimum.
      this.syncPaneHeights();
      return result;
    };

    const origRemoveIndicator = this.chart.removeIndicator.bind(this.chart);
    this.chart.removeIndicator = (filter, name) => {
      let result;
      if (typeof filter === "string" && typeof name === "string") {
        result = origRemoveIndicator({ paneId: filter, name: name });
      } else if (typeof filter === "string") {
        result = origRemoveIndicator({ name: filter });
      } else {
        result = origRemoveIndicator(filter);
      }
      // Freed height goes back to the remaining panes.
      this.syncPaneHeights();
      return result;
    };

    this.chart.setTimezone("Asia/Kolkata");
    this.chart.setSymbol({ ticker: container.id || "CHART", pricePrecision: 2, volumePrecision: 0 });
    this.chart.setPeriod({ span: 1, type: "minute" });

    this.chart.setDataLoader({
      getBars: ({ callback }) => {
        this.loaderCallback = callback;
        callback(this.formatCandles(this.data), { forward: false, backward: false });
      },
      subscribeBar: ({ callback }) => {
        this.subscriberCallback = callback;
      },
    });

    if (showVolume) {
      try {
        this.chart.createIndicator("VOL", true, { id: "candle_pane" });
      } catch (e) {
        console.warn("createIndicator VOL:", e);
      }
    }

    if (this.showTheta) {
      this.ensureThetaIndicator();
    }

    if (this.showEMA) {
      try {
        this.chart.createIndicator(
          { name: "EMA", calcParams: [10, 20, 200], paneId: "candle_pane" },
          true,
          { id: "candle_pane" }
        );
      } catch (e) {
        console.warn("createIndicator EMA:", e);
      }
    }

    if (this.showVWAP) {
      this.ensureVWAPIndicator();
    }

    if (this.showRSI) {
      this.ensureRSIIndicator();
    }

    if (this.showSupertrend) {
      this.ensureSupertrendIndicator();
    }

    if (this.showATR) {
      this.ensureATRIndicator();
    }

    if (this.showOIPane) {
      this.ensureOIIndicator();
    }

    if (this.showOIDeltaPane) {
      this.ensureOIDeltaIndicator();
    }

    if (this.container) {
      this.container.addEventListener("wheel", () => {
        if (this.showDepth) this.debounceDepthRefresh();
      }, { passive: true });
    }

    try {
      this.chart?.subscribeAction?.("onVisibleRangeChange", () => {
        if (this.showDepth) this.debounceDepthRefresh();
      });
    } catch (_) {}

    this.initLevelDragging();
    // Budget the initial pane stack too: on a short container the default
    // 100px indicator panes would otherwise hide the candles until the first
    // resize event.
    this.syncPaneHeights();
  }

  formatCandle(c) {
    if (!c || !Number.isFinite(c.time)) return null;
    const ts = c.time > 1e11 ? c.time : c.time * 1000;
    let o = Number(c.open);
    let h = Number(c.high);
    let l = Number(c.low);
    let cl = Number(c.close);

    if (!Number.isFinite(o) || !Number.isFinite(h) || !Number.isFinite(l) || !Number.isFinite(cl)) return null;
    if (o <= 0 && h <= 0 && l <= 0 && cl <= 0) return null;

    // Sanitize zero / non-positive prices from upstream broker glitches
    if (l <= 0) {
      const pos = [o, h, cl].filter(v => v > 0);
      l = pos.length ? Math.min(...pos) : 0;
    }
    if (o <= 0) o = l > 0 ? l : cl;
    if (cl <= 0) cl = o > 0 ? o : l;
    if (h <= 0 || h < Math.max(o, l, cl)) h = Math.max(o, l, cl);
    if (l <= 0 || o <= 0 || cl <= 0 || h <= 0) return null;

    const formatted: any = {
      timestamp: ts,
      open: o,
      high: Math.max(h, o, cl),
      low: Math.min(l, o, cl),
      close: cl,
      volume: Number.isFinite(c.volume) && c.volume >= 0 ? Number(c.volume) : 0,
      turnover: Number.isFinite(c.turnover) && c.turnover >= 0 ? Number(c.turnover) : 0,
      dayVolume: c.dayVolume,
    };
    if (Number.isFinite(c.tdCE)) formatted.tdCE = Number(c.tdCE);
    if (Number.isFinite(c.tdPE)) formatted.tdPE = Number(c.tdPE);
    if (Number.isFinite(Number(c.oi)) && Number(c.oi) > 0) formatted.oi = Number(c.oi);
    // Session VWAP projected from the 1-minute base series (see vwapFromBase).
    if (Number.isFinite(c.vwap)) formatted.vwap = Number(c.vwap);
    return formatted;
  }

  formatCandles(candles) {
    if (!Array.isArray(candles)) return [];
    const result = [];
    for (const c of candles) {
      const item = this.formatCandle(c);
      if (item && item.low > 0 && item.open > 0 && item.close > 0 && item.high >= item.low) {
        result.push(item);
      }
    }
    return result;
  }

  toggleEMA(forceState) {
    this.showEMA = typeof forceState === "boolean" ? forceState : !this.showEMA;
    if (!this.chart) return this.showEMA;
    if (this.showEMA) {
      try {
        this.chart.createIndicator(
          { name: "EMA", calcParams: [10, 20, 200], paneId: "candle_pane" },
          true,
          { id: "candle_pane" }
        );
      } catch (e) {
        console.warn("createIndicator EMA:", e);
      }
      this.refreshEMACrosses();
    } else {
      try {
        this.chart.removeIndicator({ paneId: "candle_pane", name: "EMA" });
      } catch (e) {
        console.warn("removeIndicator EMA:", e);
      }
      this.clearEMACrosses();
    }
    return this.showEMA;
  }

  /** @deprecated use toggleEMA */
  toggleMA(forceState) {
    return this.toggleEMA(forceState);
  }

  ensureSupertrendIndicator() {
    if (!this.chart) return;
    try {
      this.chart.createIndicator(
        { name: "SUPERTREND", calcParams: [10, 3], paneId: "candle_pane" },
        true,
        { id: "candle_pane" },
      );
    } catch (e) {
      console.warn("createIndicator SUPERTREND:", e);
    }
  }

  toggleSupertrend(forceState) {
    this.showSupertrend = typeof forceState === "boolean" ? forceState : !this.showSupertrend;
    if (!this.chart) return this.showSupertrend;
    if (this.showSupertrend) {
      this.ensureSupertrendIndicator();
    } else {
      try {
        this.chart.removeIndicator({ paneId: "candle_pane", name: "SUPERTREND" });
      } catch (e) {
        console.warn("removeIndicator SUPERTREND:", e);
      }
    }
    return this.showSupertrend;
  }

  ensureATRIndicator() {
    if (!this.chart) return;
    try {
      this.chart.createIndicator(
        { name: "ATR", calcParams: [14], paneId: "pane_atr" },
        false,
        { id: "pane_atr", height: 90 },
      );
    } catch (e) {
      console.warn("createIndicator ATR:", e);
    }
  }

  toggleATR(forceState) {
    this.showATR = typeof forceState === "boolean" ? forceState : !this.showATR;
    if (!this.chart) return this.showATR;
    if (this.showATR) {
      this.ensureATRIndicator();
    } else {
      try {
        this.chart.removeIndicator({ paneId: "pane_atr", name: "ATR" });
      } catch (e) {
        console.warn("removeIndicator ATR:", e);
      }
    }
    // A pane was added/removed: re-budget the stack so the candle pane keeps
    // its minimum height on short containers.
    this.schedule();
    return this.showATR;
  }

  ensureOIIndicator() {
    if (!this.chart) return;
    try {
      this.chart.createIndicator(
        { name: "OI", calcParams: [], paneId: "pane_oi" },
        false,
        { id: "pane_oi", height: 90 },
      );
    } catch (e) {
      console.warn("createIndicator OI:", e);
    }
  }

  toggleOIPane(forceState) {
    this.showOIPane = typeof forceState === "boolean" ? forceState : !this.showOIPane;
    if (!this.chart) return this.showOIPane;
    if (this.showOIPane) {
      this.ensureOIIndicator();
    } else {
      try {
        this.chart.removeIndicator({ paneId: "pane_oi", name: "OI" });
      } catch (e) {
        console.warn("removeIndicator OI:", e);
      }
    }
    this.schedule();
    return this.showOIPane;
  }

  ensureOIDeltaIndicator() {
    if (!this.chart) return;
    try {
      this.chart.createIndicator(
        { name: "OIDELTA", calcParams: [], paneId: "pane_oi_delta" },
        false,
        { id: "pane_oi_delta", height: 80 },
      );
    } catch (e) {
      console.warn("createIndicator OIDELTA:", e);
    }
  }

  toggleOIDeltaPane(forceState) {
    this.showOIDeltaPane = typeof forceState === "boolean" ? forceState : !this.showOIDeltaPane;
    if (!this.chart) return this.showOIDeltaPane;
    if (this.showOIDeltaPane) {
      this.ensureOIDeltaIndicator();
    } else {
      try {
        this.chart.removeIndicator({ paneId: "pane_oi_delta", name: "OIDELTA" });
      } catch (e) {
        console.warn("removeIndicator OIDELTA:", e);
      }
    }
    this.schedule();
    return this.showOIDeltaPane;
  }

  /** Latest open interest in the loaded series (0 when the pane has no OI). */
  latestOI() {
    const data = this.data || [];
    for (let i = data.length - 1; i >= 0; i--) {
      const value = Number(data[i]?.oi);
      if (Number.isFinite(value) && value > 0) return value;
    }
    return 0;
  }

  /** Wilder ATR over the bars currently loaded, in the chart's own price units. */
  getATR(period = 14) {
    return atrValue(this.data || [], period);
  }

  ensureVWAPIndicator() {
    if (!this.chart) return;
    try {
      this.chart.createIndicator(
        { id: "vwap", name: "VWAP", calcParams: [], paneId: "candle_pane" },
        true,
        { id: "candle_pane" }
      );
    } catch (e) {
      console.warn("createIndicator VWAP:", e);
    }
  }

  toggleVWAP(forceState) {
    this.showVWAP = typeof forceState === "boolean" ? forceState : !this.showVWAP;
    if (!this.chart) return this.showVWAP;
    if (this.showVWAP) {
      this.ensureVWAPIndicator();
    } else {
      try {
        this.chart.removeIndicator({ paneId: "candle_pane", name: "VWAP" });
      } catch (e) {
        console.warn("removeIndicator VWAP:", e);
      }
    }
    return this.showVWAP;
  }

  ensureRSIIndicator() {
    if (!this.chart) return;
    try {
      this.chart.createIndicator(
        { name: "RSI14", calcParams: [14], paneId: "pane_rsi" },
        false,
        { id: "pane_rsi", height: 110 }
      );
    } catch (e) {
      console.warn("createIndicator RSI:", e);
    }
  }

  toggleRSI(forceState) {
    this.showRSI = typeof forceState === "boolean" ? forceState : !this.showRSI;
    if (!this.chart) return this.showRSI;
    if (this.showRSI) {
      this.ensureRSIIndicator();
    } else {
      try {
        this.chart.removeIndicator({ paneId: "pane_rsi", name: "RSI14" });
      } catch (e) {
        console.warn("removeIndicator RSI:", e);
      }
    }
    return this.showRSI;
  }

  toggleDivergence(forceState) {
    this.showDivergence = typeof forceState === "boolean" ? forceState : !this.showDivergence;
    this.refreshDivergence();
    return this.showDivergence;
  }

  clearDivergence() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "rsi_divergence" });
    } catch (_) {}
    for (const id of this.divergenceOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.divergenceOverlayIds = [];
  }

  refreshDivergence() {
    this.clearDivergence();
    if (!this.showDivergence || !this.chart || this.data.length < 20) return;

    const rsi = Indicators.rsiSeries(this.data, 14);
    const divergences = Indicators.detectDivergences(this.data, rsi, { lookback: 150, span: 3, minRsiDelta: 1, max: 4 });

    for (const divergence of divergences) {
      try {
        const id = this.chart.createOverlay({
          name: "divergenceMarker",
          groupId: "rsi_divergence",
          points: [
            { timestamp: toChartTimestamp(divergence.from.time), value: divergence.from.price },
            { timestamp: toChartTimestamp(divergence.to.time), value: divergence.to.price },
          ],
          lock: true,
          extendData: { kind: divergence.kind },
        });
        if (id) this.divergenceOverlayIds.push(id);
      } catch (_) {}
    }
  }

  toggleMomentumCandles(forceState) {
    this.showMomentum = typeof forceState === "boolean" ? forceState : !this.showMomentum;
    this.refreshMomentumCandles();
    return this.showMomentum;
  }

  clearMomentumCandles() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "momentum_candle" });
    } catch (_) {}
    for (const id of this.momentumOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.momentumOverlayIds = [];
  }

  refreshMomentumCandles() {
    this.clearMomentumCandles();
    if (!this.showMomentum || !this.chart || this.data.length < 20) return;

    const atr = Indicators.atrSeries(this.data, 14);
    const candles = Indicators.detectMomentumCandles(this.data, atr, {
      lookback: 150,
      multiplier: 1.5,
      maxBodyRatio: 0.6,
      max: 30,
    });

    for (const candle of candles) {
      try {
        const id = this.chart.createOverlay({
          name: "signalMarker",
          groupId: "momentum_candle",
          points: [{ timestamp: toChartTimestamp(candle.time), value: candle.price }],
          lock: true,
          extendData: {
            dir: candle.kind === "bull" ? "up" : "down",
            color: candle.kind === "bull" ? "#29b360" : "#f05a5a",
            offset: 8,
            size: 3,
          },
        });
        if (id) this.momentumOverlayIds.push(id);
      } catch (_) {}
    }
  }

  ensureThetaIndicator() {
    if (!this.chart) return;
    try {
      this.chart.createIndicator(
        { name: "TD", calcParams: [], paneId: "pane_theta" },
        false,
        { id: "pane_theta", height: 90 }
      );
    } catch (e) {
      console.warn("createIndicator TD:", e);
    }
  }

  toggleTheta(forceState) {
    this.showTheta = typeof forceState === "boolean" ? forceState : !this.showTheta;
    if (!this.chart) return this.showTheta;
    if (this.showTheta) {
      this.ensureThetaIndicator();
    } else {
      try {
        this.chart.removeIndicator({ paneId: "pane_theta", name: "TD" });
      } catch (e) {
        console.warn("removeIndicator TD:", e);
      }
    }
    return this.showTheta;
  }

  setThetaSeries(byTime, { full = false } = {}) {
    this.thetaMeta = null;
    for (const candle of this.data) {
      const point = byTime?.get?.(candle.time);
      candle.tdCE = point && Number.isFinite(point.ce) ? point.ce : null;
      candle.tdPE = point && Number.isFinite(point.pe) ? point.pe : null;
    }
    if (!this.showTheta) return;
    if (full || !this.thetaDataReady) {
      this.thetaDataReady = true;
      this.setData(this.data, { resetScale: false });
      this.thetaDataReady = true;
      return;
    }
    const latest = this.data[this.data.length - 1];
    const formatted = latest ? this.formatCandle(latest) : null;
    if (formatted && this.subscriberCallback) this.subscriberCallback(formatted);
  }

  setThetaMeta(meta) {
    this.thetaMeta = meta || null;
  }

  toggleMaxPain(forceState) {
    this.showMaxPain = typeof forceState === "boolean" ? forceState : !this.showMaxPain;
    this.refreshMaxPain();
    return this.showMaxPain;
  }

  setMaxPain(value, meta = null) {
    this.maxPainValue = Number.isFinite(value) && value > 0 ? value : null;
    this.maxPainMeta = meta || null;
    this.refreshMaxPain();
  }

  clearMaxPain() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "max_pain" });
    } catch (_) {}
    try {
      this.chart.removeOverlay({ name: "maxPainLine" });
    } catch (_) {}
  }

  refreshMaxPain() {
    this.clearMaxPain();
    if (!this.showMaxPain || !this.chart || !this.maxPainValue || !this.data.length) return;
    try {
      this.chart.createOverlay({
        name: "maxPainLine",
        groupId: "max_pain",
        points: [{ value: this.maxPainValue }],
        lock: true,
        extendData: {
          price: this.maxPainValue,
          color: this.colors.accent,
          depthVisible: Boolean(this.showDepth),
        },
      });
    } catch (e) {
      console.warn("createOverlay maxPainLine:", e);
    }
  }

  /**
   * Draw the option-chain walls (max CE/PE OI and the biggest intraday OI
   * build-up) as horizontal levels. They only make sense on the spot pane,
   * where strike prices are the same scale as the plotted index.
   */
  setOIWalls(walls) {
    this.oiWalls = Array.isArray(walls) ? walls : [];
    this.refreshOIWalls();
    return this.oiWalls;
  }

  toggleOIWalls(forceState) {
    this.showOIWalls = typeof forceState === "boolean" ? forceState : !this.showOIWalls;
    this.refreshOIWalls();
    return this.showOIWalls;
  }

  clearOIWalls() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "oi_walls" });
    } catch (_) {}
    for (const id of this.oiWallOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.oiWallOverlayIds = [];
  }

  refreshOIWalls() {
    this.clearOIWalls();
    if (!this.showOIWalls || !this.chart || !this.oiWalls.length) return;
    for (const wall of this.oiWalls) {
      const isDelta = wall.metric === "doi";
      const side = wall.side === "BOTH" ? "CE+PE" : wall.side;
      const strike = Math.round(Number(wall.strike)).toLocaleString("en-IN");
      const amount = fmtCompact(Math.abs(Number(wall.value) || 0));
      const signed = `${Number(wall.value) > 0 ? "+" : Number(wall.value) < 0 ? "−" : ""}${amount}`;
      const label = isDelta
        ? `${side} ΔOI ${strike} · ${signed}`
        : `${side} OI ${strike} · ${amount}`;
      const color = wall.side === "BOTH" ? "#f0b429" : wall.side === "CE" ? "#f05a5a" : "#29b360";
      const stack = this.oiWalls.filter((item) => item.strike === wall.strike);
      const stackIndex = stack.indexOf(wall);
      try {
        const id = this.chart.createOverlay({
          name: "oiWallLine",
          groupId: "oi_walls",
          points: [{ value: wall.strike }],
          lock: true,
          extendData: {
            price: wall.strike,
            label,
            color,
            metric: wall.metric,
            depthVisible: Boolean(this.showDepth),
            labelOffset: stack.length > 1 ? (stackIndex === 0 ? -8 : 8) : 0,
          },
        });
        if (id) this.oiWallOverlayIds.push(id);
      } catch (e) {
        console.warn("createOverlay oiWallLine:", e);
      }
    }
  }

  refreshIndicatorOverlays() {
    const last = this.data[this.data.length - 1];
    const signature = `${this.data.length}:${last?.time || 0}:${last?.close || 0}`;
    if (signature === this.indicatorSignature) return;
    this.indicatorSignature = signature;
    this.refreshEMACrosses();
    this.refreshDivergence();
    this.refreshMomentumCandles();
  }

  toggleVolumeProfile(forceState) {
    if (this._vpTimer) {
      clearTimeout(this._vpTimer);
      this._vpTimer = null;
    }
    this._vpLastRefresh = 0;
    this.showVolumeProfile = typeof forceState === "boolean" ? forceState : !this.showVolumeProfile;
    this.vpLastKey = "";
    this.refreshVolumeProfile(true);
    return this.showVolumeProfile;
  }

  clearVolumeProfile() {
    this.vpOverlayId = null;
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "volume_profile" });
    } catch (_) {}
    try {
      this.chart.removeOverlay({ name: "volumeProfile" });
    } catch (_) {}
  }

  emitVolumeProfile() {
    if (!this.onVolumeProfile) return;
    try {
      this.onVolumeProfile(this.vpSuggestion);
    } catch (e) {
      console.warn("onVolumeProfile handler:", e);
    }
  }

  // Throttle with a trailing edge: ticks can arrive far faster than the redraw
  // budget, so a pure debounce would never fire during an active session.
  scheduleVolumeProfile() {
    if (this._vpTimer) return;
    const now = Date.now();
    const elapsed = now - (this._vpLastRefresh || 0);
    const wait = elapsed >= VOLUME_PROFILE_REFRESH_MS ? 0 : VOLUME_PROFILE_REFRESH_MS - elapsed;
    this._vpTimer = setTimeout(() => {
      this._vpTimer = null;
      this._vpLastRefresh = Date.now();
      if (this.showVolumeProfile) this.refreshVolumeProfile();
    }, wait);
  }

  refreshVolumeProfile(force = false) {
    if (!this.showVolumeProfile || !this.chart || this.data.length < 5) {
      this.clearVolumeProfile();
      this.vpSuggestion = null;
      this.vpLastKey = "";
      this.emitVolumeProfile();
      return;
    }
    const profile = buildVolumeProfile(this.data, { buckets: 48, lookback: 300 });
    const last = this.data[this.data.length - 1];
    const suggestion = profile ? suggestVolumeRanges(profile, last?.close) : null;
    if (!profile || !suggestion) {
      this.clearVolumeProfile();
      this.vpSuggestion = null;
      this.vpLastKey = "";
      this.emitVolumeProfile();
      return;
    }
    // The key covers the levels AND the distribution: without the volume sum
    // and the last bar time the histogram could keep stale bars while the
    // actionable lines look current.
    const key = [
      profile.candles,
      last?.time || 0,
      Math.round(profile.total),
      profile.poc.toFixed(4),
      profile.vah.toFixed(4),
      profile.val.toFixed(4),
      suggestion.bias,
    ].join(":");
    if (!force && key === this.vpLastKey && this.vpOverlayId) return;
    this.vpLastKey = key;
    this.clearVolumeProfile();
    try {
      const id = this.chart.createOverlay({
        name: "volumeProfile",
        groupId: "volume_profile",
        points: [{ value: profile.high }, { value: profile.low }],
        lock: true,
        extendData: {
          priceHigh: profile.high,
          priceLow: profile.low,
          nodes: profile.nodes,
          bias: suggestion.bias,
          colors: {
            poc: this.colors.accent,
            valueArea: this.colors.text,
          },
          poc: profile.poc,
          vah: profile.vah,
          val: profile.val,
          buy: suggestion.buy,
          sell: suggestion.sell,
        },
      });
      if (id) this.vpOverlayId = id;
    } catch (e) {
      console.warn("createOverlay volumeProfile:", e);
    }
    this.vpSuggestion = { ...suggestion, profile };
    this.emitVolumeProfile();
  }

  clearEMACrosses() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "ema_cross" });
    } catch (_) {}
    for (const id of this.emaCrossOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.emaCrossOverlayIds = [];
  }

  refreshEMACrosses() {
    this.clearEMACrosses();
    if (!this.showEMA || !this.chart || this.data.length < 25) return;

    const closes = this.data.map((candle) => candle.close);
    const fast = Indicators.emaSeries(closes, 10);
    const slow = Indicators.emaSeries(closes, 20);
    const crosses = Indicators.detectEmaCrosses(this.data, fast, slow, { lookback: 150, max: 12 });

    for (const cross of crosses) {
      const bull = cross.kind === "bull";
      try {
        const id = this.chart.createOverlay({
          name: "signalMarker",
          groupId: "ema_cross",
          points: [{ timestamp: toChartTimestamp(cross.time), value: cross.price }],
          lock: true,
          extendData: {
            dir: bull ? "up" : "down",
            color: bull ? "#29b360" : "#f05a5a",
            label: bull ? "10/20 \u2191" : "10/20 \u2193",
            offset: 12,
            size: 4,
          },
        });
        if (id) this.emaCrossOverlayIds.push(id);
      } catch (_) {}
    }
  }

  toggleDepth(forceState) {
    this.showDepth = typeof forceState === "boolean" ? forceState : !this.showDepth;
    if (!this.showDepth) {
      this.clearDepth();
    } else {
      this.refreshDepth();
    }
    // The max-pain tag sits beside the ladder; keep it in step with the toggle.
    this.refreshMaxPain();
    this.refreshOIWalls();
    return this.showDepth;
  }

  clearDepth() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "depth_bars" });
    } catch (_) {}
    for (const id of this.depthOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.depthOverlayIds = [];
  }

  getVisiblePriceRange() {
    if (!this.chart || !this.container) return null;
    let minP = Infinity;
    let maxP = -Infinity;
    const height = this.container.clientHeight || 500;

    // 1. Calculate directly from visible candles on screen using getVisibleRange()
    try {
      const vr = typeof this.chart.getVisibleRange === "function" ? this.chart.getVisibleRange() : null;
      if (vr && Array.isArray(this.data) && this.data.length > 0) {
        const from = Math.max(0, Math.floor(vr.from != null ? vr.from : 0));
        const to = Math.min(this.data.length - 1, Math.ceil(vr.to != null ? vr.to : (this.data.length - 1)));
        for (let i = from; i <= to; i++) {
          const c = this.data[i];
          if (c) {
            const l = Number(c.low);
            const h = Number(c.high);
            if (l > 0 && l < minP) minP = l;
            if (h > 0 && h > maxP) maxP = h;
          }
        }
      }
    } catch (_) {}

    // 2. Fallback to recent slice of candles if visible range didn't produce valid bounds
    if (!Number.isFinite(minP) || !Number.isFinite(maxP) || maxP <= minP) {
      if (Array.isArray(this.data) && this.data.length > 0) {
        const slice = this.data.slice(-45);
        for (const c of slice) {
          const l = Number(c.low);
          const h = Number(c.high);
          if (l > 0 && l < minP) minP = l;
          if (h > 0 && h > maxP) maxP = h;
        }
      }
    }

    if (Number.isFinite(minP) && Number.isFinite(maxP) && maxP > minP && minP > 0) {
      const pad = (maxP - minP) * 0.05;
      return { min: Math.max(0.1, minP - pad), max: maxP + pad, height };
    }
    return null;
  }

  debounceDepthRefresh() {
    if (this._depthTimer) clearTimeout(this._depthTimer);
    this._depthTimer = setTimeout(() => {
      this._depthTimer = null;
      if (this.showDepth) this.refreshDepth();
    }, 120);
  }

  setDepth(depth) {
    this.depthData = depth || null;
    this.debounceDepthRefresh();
  }

  setChainData(chain) {
    this.chainData = Array.isArray(chain) ? chain : [];
    if (this.showDepth) this.debounceDepthRefresh();
  }

  refreshDepth() {
    this.clearDepth();
    if (!this.showDepth || !this.chart) return;
    if (!this.data.length && !this.depthData) return;

    const range = this.getVisiblePriceRange();
    if (!range || range.max <= range.min) return;

    const step = computeDOMInterval(range.min, range.max, range.height);
    const { buckets, maxVol } = calculateOrderFlowBuckets(this.data, this.depthData, this.chainData, range.min, range.max, step);
    if (!buckets.length) return;

    for (const b of buckets) {
      if (b.price <= 0 || (b.buyVol <= 0 && b.sellVol <= 0)) continue;
      try {
        const id = this.chart.createOverlay({
          name: "depthLevelBar",
          groupId: "depth_bars",
          paneId: "candle_pane",
          points: [{ value: b.price }],
          lock: true,
          extendData: {
            price: b.price,
            buyVol: b.buyVol,
            sellVol: b.sellVol,
            buyOrders: b.buyOrders,
            sellOrders: b.sellOrders,
            maxVol,
            step,
            rowColors: domRowColors(this.colors?.bg),
          },
        });
        if (id) this.depthOverlayIds.push(id);
      } catch (_) {}
    }
  }

  toggleSR(forceState) {
    this.showSR = typeof forceState === "boolean" ? forceState : !this.showSR;
    this.refreshSRLines();
    return this.showSR;
  }

  clearSRLines() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "sr" });
    } catch (_) {}
    for (const id of this.srOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.srOverlayIds = [];
  }

  refreshSRLines() {
    this.clearSRLines();
    if (!this.showSR || !this.data.length || !this.chart) return;

    const { supports, resistances } = calculateSRLevels(this.data);
    for (let i = 0; i < resistances.length; i++) {
      const price = resistances[i];
      try {
        const id = this.chart.createOverlay({
          name: "horizontalStraightLine",
          groupId: "sr",
          points: [{ value: price }],
          lock: true,
          styles: {
            line: { color: this.colors.resistance, style: "dashed", size: 1 },
            text: { color: this.colors.resistance, size: 10 },
          },
        });
        if (id) this.srOverlayIds.push(id);
      } catch (_) {}
    }

    for (let i = 0; i < supports.length; i++) {
      const price = supports[i];
      try {
        const id = this.chart.createOverlay({
          name: "horizontalStraightLine",
          groupId: "sr",
          points: [{ value: price }],
          lock: true,
          styles: {
            line: { color: this.colors.support, style: "dashed", size: 1 },
            text: { color: this.colors.support, size: 10 },
          },
        });
        if (id) this.srOverlayIds.push(id);
      } catch (_) {}
    }
  }

  toggleFib(forceState) {
    this.showFib = typeof forceState === "boolean" ? forceState : !this.showFib;
    this.refreshFib();
    return this.showFib;
  }

  clearFib() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "fib_retracement" });
    } catch (_) {}
    for (const id of this.fibOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.fibOverlayIds = [];
  }

  refreshFib() {
    this.clearFib();
    if (!this.showFib || !this.chart || !this.data.length) return;
    const fib = calculateFibLevels(this.data);
    if (!fib || !fib.levels.length) return;
    try {
      const id = this.chart.createOverlay({
        name: "fibRetracement",
        groupId: "fib_retracement",
        points: fib.levels.map((level) => ({ value: level.price })),
        lock: true,
        extendData: fib,
      });
      if (id) this.fibOverlayIds.push(id);
    } catch (e) {
      console.warn("createOverlay fibRetracement:", e);
    }
  }

  toggleAutoTrendlines(forceState) {
    this.showAutoTrendlines = typeof forceState === "boolean" ? forceState : !this.showAutoTrendlines;
    this.refreshAutoTrendlines();
    return this.showAutoTrendlines;
  }

  clearAutoTrendlines() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "auto_trendline" });
    } catch (_) {}
    for (const id of this.autoTrendlineOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.autoTrendlineOverlayIds = [];
  }

  refreshAutoTrendlines() {
    this.clearAutoTrendlines();
    if (!this.showAutoTrendlines || !this.data.length || !this.chart) return;

    const { resistance, support } = calculateAutoTrendlines(this.data);

    if (resistance) {
      try {
        const id = this.chart.createOverlay({
          name: "rayLine",
          groupId: "auto_trendline",
          points: [
            { timestamp: resistance.p1.time > 1e11 ? resistance.p1.time : resistance.p1.time * 1000, value: resistance.p1.price },
            { timestamp: resistance.p2.time > 1e11 ? resistance.p2.time : resistance.p2.time * 1000, value: resistance.p2.price },
          ],
          lock: true,
          styles: {
            line: {
              color: this.colors.resistance,
              style: "dashed",
              dashedValue: [6, 3],
              size: 1.5,
            },
            point: {
              color: this.colors.resistance,
              borderColor: "#ffffff",
              borderSize: 1,
              radius: 3,
            },
          },
        });
        if (id) this.autoTrendlineOverlayIds.push(id);
      } catch (e) {
        console.warn("createAutoTrendline resistance:", e);
      }
    }

    if (support) {
      try {
        const id = this.chart.createOverlay({
          name: "rayLine",
          groupId: "auto_trendline",
          points: [
            { timestamp: support.p1.time > 1e11 ? support.p1.time : support.p1.time * 1000, value: support.p1.price },
            { timestamp: support.p2.time > 1e11 ? support.p2.time : support.p2.time * 1000, value: support.p2.price },
          ],
          lock: true,
          styles: {
            line: {
              color: this.colors.support,
              style: "dashed",
              dashedValue: [6, 3],
              size: 1.5,
            },
            point: {
              color: this.colors.support,
              borderColor: "#ffffff",
              borderSize: 1,
              radius: 3,
            },
          },
        });
        if (id) this.autoTrendlineOverlayIds.push(id);
      } catch (e) {
        console.warn("createAutoTrendline support:", e);
      }
    }
  }

  togglePatterns(forceState) {
    this.showPatterns = typeof forceState === "boolean" ? forceState : !this.showPatterns;
    this.refreshPatterns();
    return this.showPatterns;
  }

  setPatternConfig(config) {
    this.patternConfig = { ...this.patternConfig, ...config };
    if (this.showPatterns) {
      this.refreshPatterns();
    }
  }

  clearPatterns() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "pattern_candlestick" });
      this.chart.removeOverlay({ groupId: "pattern_geometric" });
      this.chart.removeOverlay({ groupId: "pattern_harmonic" });
      this.chart.removeOverlay({ groupId: "pattern_liquidity" });
    } catch (_) {}
    for (const id of this.patternOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.patternOverlayIds = [];
  }

  refreshPatterns() {
    this.clearPatterns();
    if (!this.showPatterns || !this.data.length || !this.chart) return;

    const detected = Patterns.detectAllPatterns(this.data, {
      candlestick: this.patternConfig.candlestick !== false,
      geometric: this.patternConfig.geometric !== false,
      harmonic: this.patternConfig.harmonic !== false,
      liquidity: this.patternConfig.liquidity !== false,
      lookback: 90,
    });

    // 1. Candlestick overlays
    for (const p of (detected.candlestick || [])) {
      try {
        const id = this.chart.createOverlay({
          name: "candlestickPatternMarker",
          groupId: "pattern_candlestick",
          points: [{
            timestamp: p.time > 1e11 ? p.time : p.time * 1000,
            value: p.price,
          }],
          lock: true,
          extendData: p,
        });
        if (id) this.patternOverlayIds.push(id);
      } catch (e) {
        console.warn("createOverlay candlestick:", e);
      }
    }

    // 2. Geometric overlays
    for (const p of (detected.geometric || [])) {
      try {
        let points = [];
        if (Array.isArray(p.points)) {
          points = p.points.map(pt => ({
            timestamp: pt.time > 1e11 ? pt.time : pt.time * 1000,
            value: pt.price,
          }));
        } else if (p.resistance && p.support) {
          points = [
            { timestamp: p.resistance.p1.time > 1e11 ? p.resistance.p1.time : p.resistance.p1.time * 1000, value: p.resistance.p1.price },
            { timestamp: p.resistance.p2.time > 1e11 ? p.resistance.p2.time : p.resistance.p2.time * 1000, value: p.resistance.p2.price },
            { timestamp: p.support.p1.time > 1e11 ? p.support.p1.time : p.support.p1.time * 1000, value: p.support.p1.price },
            { timestamp: p.support.p2.time > 1e11 ? p.support.p2.time : p.support.p2.time * 1000, value: p.support.p2.price },
          ];
        }
        if (!points.length) continue;

        const id = this.chart.createOverlay({
          name: "geometricPatternOverlay",
          groupId: "pattern_geometric",
          points,
          lock: true,
          extendData: p,
        });
        if (id) this.patternOverlayIds.push(id);
      } catch (e) {
        console.warn("createOverlay geometric:", e);
      }
    }

    // 3. Harmonic overlays
    for (const p of (detected.harmonic || [])) {
      try {
        const points = (p.points || []).map(pt => ({
          timestamp: pt.time > 1e11 ? pt.time : pt.time * 1000,
          value: pt.price,
        }));
        if (!points.length) continue;

        const id = this.chart.createOverlay({
          name: "harmonicPatternOverlay",
          groupId: "pattern_harmonic",
          points,
          lock: true,
          extendData: p,
        });
        if (id) this.patternOverlayIds.push(id);
      } catch (e) {
        console.warn("createOverlay harmonic:", e);
      }
    }

    // 4. Liquidity overlays
    for (const p of (detected.liquidity || [])) {
      try {
        const id = this.chart.createOverlay({
          name: "liquidityPatternOverlay",
          groupId: "pattern_liquidity",
          points: [{
            timestamp: p.time > 1e11 ? p.time : p.time * 1000,
            value: p.levelPrice || p.sweepPrice || 0,
          }],
          lock: true,
          extendData: p,
        });
        if (id) this.patternOverlayIds.push(id);
      } catch (e) {
        console.warn("createOverlay liquidity:", e);
      }
    }
  }

  toggleSignals(forceState) {
    this.showSignals = typeof forceState === "boolean" ? forceState : !this.showSignals;
    if (!this.showSignals) this.clearSignals();
    return this.showSignals;
  }

  clearSignals() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "signal_engine" });
    } catch (_) {}
    for (const id of this.signalOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.signalOverlayIds = [];
  }

  /**
   * Render the deterministic signal engine's per-bar results as chart
   * markers. Results come from the engine's fixed evaluation grid (5m); a
   * marker's timestamp snaps onto whatever displayed bar contains it, so the
   * markers stay meaningful on every chart timeframe. Only a bounded recent
   * window is drawn, and just the last few ARMED previews to avoid clutter.
   */
  setSignals(results) {
    this.clearSignals();
    if (!this.showSignals || !this.chart || !Array.isArray(results) || !results.length) return;
    const recent = results.slice(-120);
    let armedDrawn = 0;
    for (const result of recent) {
      if (!result || !Number.isFinite(result.time)) continue;
      const buy = result.action === "BUY_CE" || result.action === "BUY_PE";
      const armed = result.action === "ARMED_CE" || result.action === "ARMED_PE";
      if (!buy && !armed) continue;
      const bull = result.action === "BUY_CE" || result.action === "ARMED_CE";
      if (armed && armedDrawn >= 3) continue;
      if (armed) armedDrawn += 1;
      const bar = findClosestCandle(this.data, toChartTimestamp(result.time));
      const price = bull
        ? Number(result.stop ?? (bar ? bar.low : NaN))
        : Number(result.stop ?? (bar ? bar.high : NaN));
      if (!Number.isFinite(price)) continue;
      try {
        const id = this.chart.createOverlay({
          name: "signalMarker",
          groupId: "signal_engine",
          points: [{ timestamp: toChartTimestamp(result.time), value: price }],
          lock: true,
          extendData: {
            dir: bull ? "up" : "down",
            color: armed ? "#f0b429" : bull ? "#29b360" : "#f05a5a",
            label: armed ? "ARMED" : bull ? "CE" : "PE",
            offset: armed ? 8 : 10,
            size: armed ? 3 : 4,
          },
        });
        if (id) this.signalOverlayIds.push(id);
      } catch (e) {
        console.warn("createOverlay signal:", e);
      }
    }
  }

  toggleTrendline(button: any, { onStart, onComplete, onCancel }: any = {}) {
    if (!this.chart) return null;
    if (this.drawingTrendlineId) {
      this.cancelTrendline();
      if (onCancel) onCancel();
      return null;
    }

    const frame = this.container.closest(".chart-frame") || this.container;
    const lineColor = this.colors.accent || "#f0b429";

    if (button) {
      button.classList.add("active");
      button.setAttribute("aria-pressed", "true");
    }
    if (frame) frame.classList.add("drawing-mode");

    const escHandler = (e) => {
      if (e.key === "Escape" && this.drawingTrendlineId) {
        this.cancelTrendline();
        if (onCancel) onCancel();
      }
    };
    window.addEventListener("keydown", escHandler);

    try {
      const id = this.chart.createOverlay({
        name: "segment",
        styles: {
          line: {
            color: lineColor,
            size: 2,
            style: "solid",
          },
          point: {
            color: lineColor,
            borderColor: "#ffffff",
            borderSize: 1.5,
            radius: 5,
            activeColor: "#ffffff",
            activeBorderColor: lineColor,
            activeBorderSize: 2,
            activeRadius: 6,
          },
        },
        onDrawEnd: () => {
          this.drawingTrendlineId = null;
          this.drawingTrendlineBtn = null;
          if (this.drawingEscHandler) {
            window.removeEventListener("keydown", this.drawingEscHandler);
            this.drawingEscHandler = null;
          }
          if (button) {
            button.classList.remove("active");
            button.setAttribute("aria-pressed", "false");
          }
          if (frame) frame.classList.remove("drawing-mode");
          if (onComplete) onComplete();
        },
      });

      this.drawingTrendlineId = id;
      this.drawingTrendlineBtn = button;
      this.drawingEscHandler = escHandler;
      if (onStart) onStart();
      return id;
    } catch (e) {
      console.warn("createTrendline:", e);
      this.cancelTrendline();
      return null;
    }
  }

  cancelTrendline() {
    if (this.drawingEscHandler) {
      window.removeEventListener("keydown", this.drawingEscHandler);
      this.drawingEscHandler = null;
    }
    if (this.drawingTrendlineId) {
      try {
        this.chart?.removeOverlay({ id: this.drawingTrendlineId });
      } catch (_) {}
      this.drawingTrendlineId = null;
    }
    if (this.drawingTrendlineBtn) {
      this.drawingTrendlineBtn.classList.remove("active");
      this.drawingTrendlineBtn.setAttribute("aria-pressed", "false");
      this.drawingTrendlineBtn = null;
    }
    const frame = this.container?.closest(".chart-frame") || this.container;
    if (frame) frame.classList.remove("drawing-mode");
  }

  createTrendline(button, callbacks) {
    return this.toggleTrendline(button, callbacks);
  }

  resetPriceScale() {
    if (!this.chart) return;
    try {
      if (typeof this.chart._resetYAxisAutoCalcTickFlag === "function") {
        this.chart._resetYAxisAutoCalcTickFlag();
      }
      this.chart.layout?.({ measureWidth: true, update: true, buildYAxisTick: true });
    } catch (_) {}
  }

  getPriceFromPixel(pixelY, pixelX = 50, snapToTick = true) {
    if (!this.chart) return null;
    try {
      const res = this.chart.convertFromPixel({ x: pixelX, y: pixelY });
      if (res && Number.isFinite(res.value) && res.value > 0) {
        // Dragging must follow the pointer continuously; snapping to the
        // exchange tick only on drop avoids visible jumps on cheap options.
        return snapToTick ? Math.round(res.value * 20) / 20 : res.value;
      }
    } catch (e) {
      console.warn("getPriceFromPixel:", e);
    }
    return null;
  }

  /* ── Draft (pre-order) levels ─────────────────────────────
   * The quick-order card opens on a chart click and parks ENTRY/TP/SL lines on
   * the price axis. They are draggable before submission so the user can pick
   * the exact prices, then press BUY/SELL. Everything here is local UI state.
   */

  showOrderPreview(price) {
    const value = Number(price);
    this.draftLevels.entry = Number.isFinite(value) && value > 0 ? value : 0;
    this.renderDraftLevels();
  }

  clearOrderPreview() {
    this.draftLevels.entry = 0;
    this.renderDraftLevels();
  }

  showTargetPreview(price) {
    const value = Number(price);
    this.draftLevels.target = Number.isFinite(value) && value > 0 ? value : 0;
    this.renderDraftLevels();
  }

  clearTargetPreview() {
    this.draftLevels.target = 0;
    this.renderDraftLevels();
  }

  showStopPreview(price) {
    const value = Number(price);
    this.draftLevels.stop = Number.isFinite(value) && value > 0 ? value : 0;
    this.renderDraftLevels();
  }

  clearStopPreview() {
    this.draftLevels.stop = 0;
    this.renderDraftLevels();
  }

  renderDraftLevels() {
    if (!this.chart) return;
    const { entry = 0, target = 0, stop = 0, side = "B" } = this.draftLevels || {};
    const tick = 0.05;
    const round = (value) => Math.round(Number(value) * 20) / 20;
    const sideTag = String(side).toUpperCase() === "S" ? "S" : "B";
    const levels = [];
    if (Number.isFinite(entry) && entry > 0) {
      levels.push({ kind: "ENTRY", price: entry, label: `ENTRY ₹${entry.toFixed(2)} · LIMIT` });
    }
    if (Number.isFinite(target) && target > 0) {
      levels.push({ kind: "TARGET", price: target, label: `TARGET ₹${target.toFixed(2)} · DRAFT` });
    }
    if (Number.isFinite(stop) && stop > 0) {
      levels.push({ kind: "STOP", price: stop, label: `SL ₹${stop.toFixed(2)} · DRAFT` });
    }

    const key = levels.map((level) => level.label).join("|") + `|${sideTag}`;
    if (key === this.draftLevelsKey && this.draftLevelOverlayIds.length === levels.length) return;

    this.clearDraftOverlays();
    this.draftLevelsKey = key;
    for (const level of levels) {
      // Clamp each drag inside its neighbours so an entry can never cross its
      // own TP/SL. Zero means "no bound on that side".
      let minPrice = tick;
      let maxPrice = 0;
      if (level.kind === "ENTRY") {
        if (stop > 0) minPrice = round(stop + tick);
        if (target > 0) maxPrice = round(target - tick);
      } else if (level.kind === "TARGET") {
        if (entry > 0) minPrice = round(entry + tick);
      } else if (level.kind === "STOP") {
        if (entry > 0) maxPrice = round(entry - tick);
      }
      try {
        const id = this.chart.createOverlay({
          name: "bracketLevelLine",
          groupId: "draft_levels",
          points: [{ value: level.price }],
          lock: true,
          extendData: {
            source: "draft",
            kind: level.kind,
            price: level.price,
            label: level.label,
            side: sideTag,
            draggable: true,
            noRemove: true,
            minPrice,
            maxPrice: Math.max(0, maxPrice),
          },
        });
        if (id) this.draftLevelOverlayIds.push(id);
      } catch (_) {}
    }
  }

  clearDraftOverlays() {
    this.draftLevelsKey = "";
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "draft_levels" });
    } catch (_) {}
    for (const id of this.draftLevelOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.draftLevelOverlayIds = [];
  }

  /**
   * Draw the persisted entry/target/stop levels of a server-side bracket order.
   * A pending (resting entry) bracket shows its entry too; an in-position
   * bracket only needs the target/stop because the position line marks the entry.
   */
  showBracketLevels({
    bracketId = "",
    entryPrice = 0,
    targetPrice = 0,
    stopPrice = 0,
    side = "BUY",
    pending = false,
    exchangeStopPrice = 0,
    exchangeStopMode = "",
    exchangeStopStatus = "",
  } = {}) {
    this.bracketData = {
      bracketId, entryPrice, targetPrice, stopPrice, side, pending,
      exchangeStopPrice, exchangeStopMode, exchangeStopStatus,
    };
    this.renderBracketLevels();
  }

  renderBracketLevels() {
    if (!this.chart || !this.bracketData || !this.showTrades) {
      this.clearBracketOverlays();
      return;
    }
    const {
      bracketId = "", entryPrice = 0, targetPrice = 0, stopPrice = 0,
      side = "BUY", pending = false, exchangeStopPrice = 0,
      exchangeStopMode = "", exchangeStopStatus = "",
    } = this.bracketData;
    const entry = Number(entryPrice);
    const target = Number(targetPrice);
    const stop = Number(stopPrice);
    const exchangeStop = Number(exchangeStopPrice);
    const isBuy = side === "B" || side === "BUY" || side === "buy";
    const levels = [];
    if (pending && Number.isFinite(entry) && entry > 0) {
      levels.push({ kind: "ENTRY", price: entry, label: `ENTRY ₹${entry.toFixed(2)} (PENDING) · EXCH`, placement: "exchange" });
    }
    if (Number.isFinite(target) && target > 0) {
      levels.push({ kind: "TARGET", price: target, label: `TARGET ₹${target.toFixed(2)} · SRV`, placement: "server" });
    }
    if (Number.isFinite(stop) && stop > 0) {
      levels.push({ kind: "STOP", price: stop, label: `SL ₹${stop.toFixed(2)} · SRV`, placement: "server" });
    }
    if (Number.isFinite(exchangeStop) && exchangeStop > 0) {
      const type = String(exchangeStopMode || "SL-M").toUpperCase() === "SL" ? "SL-L" : "SL-M";
      const state = String(exchangeStopStatus || "").toUpperCase();
      const suffix = state && state !== "WORKING" ? ` · ${state}` : "";
      levels.push({
        kind: "EXCHANGE_STOP",
        price: exchangeStop,
        label: `${type} ₹${exchangeStop.toFixed(2)} · EXCH${suffix}`,
        placement: "exchange",
      });
    }

    const key = levels.map((l) => l.label).join("|") + `|${isBuy ? "B" : "S"}|${bracketId}`;
    if (key === this.bracketLevelsKey && this.bracketLevelOverlayIds.length === levels.length) return;

    this.clearBracketOverlays();
    this.bracketLevelsKey = key;
    for (const level of levels) {
      const draggable = level.kind !== "EXCHANGE_STOP";
      try {
        const id = this.chart.createOverlay({
          name: "bracketLevelLine",
          groupId: "bracket_levels",
          points: [{ value: level.price }],
          lock: true,
          extendData: {
            bracketId,
            kind: level.kind,
            price: level.price,
            label: level.label,
            side: level.kind === "EXCHANGE_STOP"
              ? (isBuy ? "S" : "B")
              : (isBuy ? "B" : "S"),
            placement: level.placement,
            draggable,
          },
        });
        if (id) {
          this.bracketLevelOverlayIds.push(id);
          if (draggable) {
            this.draggableBracketLevels.push({
              source: "bracket",
              bracketId,
              kind: level.kind,
              price: level.price,
              side: isBuy ? "B" : "S",
            });
          }
        }
      } catch (_) {}
    }
  }

  clearBracketOverlays() {
    this.bracketLevelsKey = "";
    this.draggableBracketLevels = [];
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "bracket_levels" });
    } catch (_) {}
    for (const id of this.bracketLevelOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.bracketLevelOverlayIds = [];
  }

  clearBracketLevels() {
    this.bracketData = null;
    this.clearBracketOverlays();
  }

  /** One dashed vertical line at the first bar of each trading day. */
  refreshDayDividers() {
    if (!this.chart) return;
    const boundaries = [];
    let previousDay = "";
    for (const candle of this.data) {
      if (!Number.isFinite(candle?.time)) continue;
      const day = istDayKey(candle.time);
      if (previousDay && day !== previousDay) boundaries.push({ time: candle.time, day });
      previousDay = day;
    }
    const key = boundaries.map((boundary) => boundary.time).join(",");
    if (key === this.dayDividerKey) return;
    this.clearDayDividers();
    this.dayDividerKey = key;
    for (const boundary of boundaries) {
      try {
        const id = this.chart.createOverlay({
          name: "dayDividerLine",
          groupId: "day_divider",
          points: [{ timestamp: toChartTimestamp(boundary.time) }],
          lock: true,
          extendData: { color: this.colors.border, day: boundary.day },
        });
        if (id) this.dayDividerOverlayIds.push(id);
      } catch (_) {}
    }
  }

  clearDayDividers() {
    this.dayDividerKey = "";
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "day_divider" });
    } catch (_) {}
    for (const id of this.dayDividerOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.dayDividerOverlayIds = [];
  }

  setData(data, { symbol = "", resetScale = true } = {}) {
    this.thetaDataReady = false;
    if (symbol && symbol !== this.currentSymbol) {
      this.currentSymbol = symbol;
      // Rebuild cached bracket overlays for the new symbol.
      this.bracketLevelOverlayIds = [];
      this.draggableBracketLevels = [];
      this.bracketLevelsKey = "";
      this.bracketData = null;
      // Draft prices belong to the previous instrument, so drop them with it.
      this.draftLevels = { entry: 0, target: 0, stop: 0, side: "B" };
      this.clearDraftOverlays();
      this.indicatorSignature = "";
      this.vpLastKey = "";
      try {
        this.chart?.setSymbol({ ticker: symbol, pricePrecision: 2, volumePrecision: 0 });
      } catch (_) {}
    }
    const byTime = new Map();
    if (Array.isArray(data)) {
      for (const candle of data) {
        if (!Number.isFinite(candle?.time) || !Number.isFinite(candle?.open) ||
            !Number.isFinite(candle?.high) || !Number.isFinite(candle?.low) ||
            !Number.isFinite(candle?.close)) continue;
        byTime.set(candle.time, candle);
      }
    }
    this.data = [...byTime.values()].sort((a, b) => a.time - b.time);
    const formatted = this.formatCandles(this.data);

    if (this.loaderCallback) {
      this.loaderCallback(formatted, { forward: false, backward: false });
    }
    try {
      this.chart?.resetData();
    } catch (_) {}

    if (resetScale) {
      this.resetPriceScale();
      try {
        this.chart?.scrollToRealTime();
      } catch (_) {}
    }

    this.refreshSRLines();
    this.refreshFib();
    this.refreshMaxPain();
    this.refreshOIWalls();
    this.refreshDayDividers();
    this.refreshAutoTrendlines();
    this.refreshPatterns();
    this.refreshIndicatorOverlays();
    this.refreshTradeOverlays();
    this.refreshPositionOverlay();
    this.refreshOpenOrders();
    this.refreshDepth();
    this.refreshVolumeProfile();
  }

  setTrades(trades, { isSpot = false } = {}) {
    this.tradesData = Array.isArray(trades) ? trades : [];
    this.isSpotChart = Boolean(isSpot);
    this.refreshTradeOverlays();
  }

  setPosition(position) {
    this.positionData = position || null;
    this.refreshPositionOverlay();
  }

  setOpenOrders(openOrders) {
    this.openOrdersData = Array.isArray(openOrders) ? openOrders : [];
    this.refreshOpenOrders();
  }

  clearOpenOrders() {
    this.draggableOrderLevels = [];
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "open_orders" });
    } catch (_) {}
    for (const id of this.openOrderOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.openOrderOverlayIds = [];
  }

  refreshOpenOrders() {
    this.clearOpenOrders();
    if (!this.showTrades || !this.chart || !this.data.length || !this.openOrdersData.length) return;
    for (const o of this.openOrdersData) {
      const price = Number(o.price);
      if (!Number.isFinite(price) || price <= 0) continue;
      // A pending bracket entry is drawn by showBracketLevels so dragging it
      // updates the bracket instead of leaving the bracket out of sync.
      if (o.bracketId) continue;
      try {
        const id = this.chart.createOverlay({
          name: "openOrderLine",
          groupId: "open_orders",
          points: [{ value: price }],
          lock: true,
          extendData: {
            orderId: o.id,
            kind: "ENTRY",
            side: o.side,
            qty: o.quantity - (Number(o.filled_quantity) || 0),
            price,
            draggable: true,
          },
        });
        if (id) {
          this.openOrderOverlayIds.push(id);
          this.draggableOrderLevels.push({
            source: "order",
            orderId: o.id,
            kind: "ENTRY",
            price,
            side: o.side,
          });
        }
      } catch (_) {}
    }
  }

  toggleTrades(forceState) {
    this.showTrades = typeof forceState === "boolean" ? forceState : !this.showTrades;
    this.refreshTradeOverlays();
    this.refreshPositionOverlay();
    this.refreshOpenOrders();
    this.renderBracketLevels();
    return this.showTrades;
  }

  initLevelDragging() {
    if (!this.container || this.levelPointerHandlers) return;
    const down = (event) => this.onLevelPointerDown(event);
    const move = (event) => this.onLevelPointerMove(event);
    const up = (event) => this.onLevelPointerUp(event);
    this.container.addEventListener("pointerdown", down, true);
    this.container.addEventListener("pointermove", move, true);
    this.container.addEventListener("pointerup", up, true);
    this.container.addEventListener("pointercancel", up, true);
    this.levelPointerHandlers = { down, move, up };
  }

  getDraggableLevels() {
    return [...this.draggableBracketLevels, ...this.draggableOrderLevels];
  }

  overlayHitTargets() {
    if (!this.chart) return [];
    const groups = ["bracket_levels", "open_orders", "draft_levels"];
    const targets = [];
    for (const groupId of groups) {
      let overlays = [];
      try {
        overlays = this.chart.getOverlays({ groupId });
      } catch (_) {
        continue;
      }
      for (const overlay of overlays || []) {
        const data = overlay?.extendData || {};
        if (!data.draggable) continue;
        targets.push(data);
      }
    }
    return targets;
  }

  resolveDraggableLevel(data) {
    if (!data) return null;
    const isOrder = data.orderId !== undefined && data.orderId !== null && data.orderId !== "";
    const list = isOrder ? this.draggableOrderLevels : this.draggableBracketLevels;
    const match = list.find((item) => {
      if (String(item.kind) !== String(data.kind)) return false;
      if (Math.abs(Number(item.price) - Number(data.price)) >= 1e-6) return false;
      return isOrder
        ? String(item.orderId) === String(data.orderId)
        : String(item.bracketId) === String(data.bracketId);
    });
    if (match) return match;
    return {
      source: data.source || (isOrder ? "order" : "bracket"),
      orderId: isOrder ? data.orderId : undefined,
      bracketId: isOrder ? undefined : data.bracketId,
      kind: data.kind,
      price: Number(data.price),
      side: data.side,
      minPrice: Number(data.minPrice) || 0,
      maxPrice: Number(data.maxPrice) || 0,
    };
  }

  /** Keep a dragged level inside the bounds baked into its overlay data. */
  clampLevelPrice(level, price) {
    let next = Number(price);
    if (!Number.isFinite(next)) return null;
    const min = Number(level?.minPrice);
    const max = Number(level?.maxPrice);
    if (Number.isFinite(min) && min > 0) next = Math.max(min, next);
    if (Number.isFinite(max) && max > 0) next = Math.min(max, next);
    return next;
  }

  /** Only the drawn grab handle starts a drag; the price badge stays clickable. */
  hitTestLevel(clientX, clientY) {
    if (!this.chart || !this.container) return null;
    const rect = this.container.getBoundingClientRect();
    const x = clientX - rect.left;
    const y = clientY - rect.top;
    if (y < 0 || y > rect.height) return null;
    for (const data of this.overlayHitTargets()) {
      const gripX = Number(data.gripX);
      const gripY = Number(data.gripY);
      if (!Number.isFinite(gripX) || !Number.isFinite(gripY)) continue;
      // Draft levels only have the grip (no remove badge), so give them a
      // slightly larger target — the card is hidden while dragging and the
      // user is aiming at a single small handle.
      const radius = data.source === "draft" ? 16 : 10;
      if (Math.abs(x - gripX) > radius || Math.abs(y - gripY) > radius) continue;
      return this.resolveDraggableLevel(data);
    }
    return null;
  }

  hitTestRemoveBadge(clientX, clientY) {
    if (!this.container || !this.chart) return null;
    const rect = this.container.getBoundingClientRect();
    const x = clientX - rect.left;
    const y = clientY - rect.top;
    for (const data of this.overlayHitTargets()) {
      const badgeX = Number(data.badgeX);
      const badgeY = Number(data.badgeY);
      if (!Number.isFinite(badgeX) || !Number.isFinite(badgeY)) continue;
      if (Math.abs(x - badgeX) > 12 || Math.abs(y - badgeY) > 12) continue;
      return this.resolveDraggableLevel(data);
    }
    return null;
  }

  levelDragLabel(level, price) {
    const kind = String(level?.kind || "ENTRY").toUpperCase();
    const prefix = kind === "TARGET" ? "MOVE TARGET" : kind === "STOP" ? "MOVE SL" : "MOVE ENTRY";
    const shown = Number(price) < 1 ? Number(price).toFixed(3) : Number(price).toFixed(2);
    return `${prefix}: ₹${shown}`;
  }

  showLevelDragPreview(level, price) {
    this.clearLevelDragPreview();
    if (!this.chart) return;
    try {
      this.levelDragPreviewId = this.chart.createOverlay({
        name: "dragLevelLine",
        groupId: "level_drag",
        points: [{ value: price }],
        lock: true,
        extendData: { price, label: this.levelDragLabel(level, price) },
      });
    } catch (_) {}
  }

  updateLevelDragPreview(level, price) {
    if (!this.levelDragPreviewId) {
      this.showLevelDragPreview(level, price);
      return;
    }
    try {
      this.chart.overrideOverlay({
        id: this.levelDragPreviewId,
        points: [{ value: price }],
        extendData: { price, label: this.levelDragLabel(level, price) },
      });
    } catch (_) {
      this.showLevelDragPreview(level, price);
    }
  }

  clearLevelDragPreview() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "level_drag" });
    } catch (_) {}
    if (this.levelDragPreviewId) {
      try {
        this.chart.removeOverlay({ id: this.levelDragPreviewId });
      } catch (_) {}
      this.levelDragPreviewId = null;
    }
  }

  onLevelPointerDown(event) {
    if (event.button !== undefined && event.button !== 0) return;
    if (this.levelDrag || this.drawingTrendlineId) return;
    const removeLevel = this.hitTestRemoveBadge(event.clientX, event.clientY);
    if (removeLevel) {
      event.preventDefault();
      event.stopPropagation();
      this.suppressChartClickUntil = Date.now() + 600;
      if (typeof this.onLevelRemove === "function") {
        try {
          this.onLevelRemove({ ...removeLevel });
        } catch (error) {
          console.warn("onLevelRemove:", error);
        }
      }
      return;
    }
    const level = this.hitTestLevel(event.clientX, event.clientY);
    if (!level) return;
    event.preventDefault();
    event.stopPropagation();
    this.levelDrag = {
      level,
      pointerId: event.pointerId,
      startY: event.clientY,
      moved: false,
      started: false,
      price: Number(level.price),
    };
    this.suppressChartClickUntil = Date.now() + 600;
    this.container.style.cursor = "grabbing";
    try {
      this.container.setPointerCapture(event.pointerId);
    } catch (_) {}
    this.showLevelDragPreview(level, Number(level.price));
  }

  onLevelPointerMove(event) {
    if (!this.container) return;
    if (!this.levelDrag) {
      if (this.hitTestRemoveBadge(event.clientX, event.clientY)) {
        this.container.style.cursor = "pointer";
      } else {
        this.container.style.cursor = this.hitTestLevel(event.clientX, event.clientY) ? "grab" : "";
      }
      return;
    }
    if (event.pointerId !== this.levelDrag.pointerId) return;
    event.preventDefault();
    event.stopPropagation();
    const rect = this.container.getBoundingClientRect();
    const y = Math.max(0, Math.min(rect.height - 1, event.clientY - rect.top));
    const x = Math.max(1, Math.min(rect.width - 1, rect.width / 2));
    const price = this.clampLevelPrice(this.levelDrag.level, this.getPriceFromPixel(y, x, false));
    if (!Number.isFinite(price) || price <= 0) return;
    if (Math.abs(event.clientY - this.levelDrag.startY) >= 2) {
      this.levelDrag.moved = true;
      this.container.style.cursor = "grabbing";
      if (!this.levelDrag.started) {
        this.levelDrag.started = true;
        // Only a real drag hides the quick-order card: a bare grip click would
        // otherwise flicker it off and steal focus from the price input.
        if (this.onLevelDragStart) {
          try {
            this.onLevelDragStart({ ...this.levelDrag.level });
          } catch (error) {
            console.warn("onLevelDragStart:", error);
          }
        }
      }
    }
    this.levelDrag.price = price;
    this.updateLevelDragPreview(this.levelDrag.level, price);
  }

  onLevelPointerUp(event) {
    if (!this.levelDrag) return;
    if (event.pointerId !== undefined && event.pointerId !== this.levelDrag.pointerId) return;
    const drag = this.levelDrag;
    this.levelDrag = null;
    this.suppressChartClickUntil = Date.now() + 400;
    if (this.container) {
      this.container.style.cursor = "";
      try {
        if (this.container.hasPointerCapture(event.pointerId)) {
          this.container.releasePointerCapture(event.pointerId);
        }
      } catch (_) {}
    }
    this.clearLevelDragPreview();
    if (drag.started && this.onLevelDragEnd) {
      try {
        this.onLevelDragEnd({ ...drag.level });
      } catch (error) {
        console.warn("onLevelDragEnd:", error);
      }
    }
    if (event.type === "pointercancel") return;
    event.preventDefault();
    event.stopPropagation();
    if (!drag.moved) return;
    const price = this.clampLevelPrice(drag.level, Math.round(Number(drag.price) * 20) / 20);
    if (!Number.isFinite(price) || price <= 0) return;
    if (typeof this.onLevelDrop === "function") {
      try {
        this.onLevelDrop({ ...drag.level, price, previousPrice: Number(drag.level.price) });
      } catch (error) {
        console.warn("onLevelDrop:", error);
      }
    }
  }

  consumeChartClickSuppression() {
    if (Date.now() < this.suppressChartClickUntil) {
      this.suppressChartClickUntil = 0;
      return true;
    }
    return false;
  }

  clearTradeOverlays() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "trades" });
    } catch (_) {}
    for (const id of this.tradeOverlayIds) {
      try {
        this.chart.removeOverlay({ id });
      } catch (_) {}
    }
    this.tradeOverlayIds = [];
  }

  clearPositionOverlay() {
    if (!this.chart) return;
    try {
      this.chart.removeOverlay({ groupId: "position" });
    } catch (_) {}
    if (this.positionOverlayId) {
      try {
        this.chart.removeOverlay({ id: this.positionOverlayId });
      } catch (_) {}
      this.positionOverlayId = null;
    }
  }

  refreshTradeOverlays() {
    this.clearTradeOverlays();
    if (!this.showTrades || !this.tradesData.length || !this.chart || !this.data.length) return;

    if (this.isSpotChart) {
      for (const trade of this.tradesData) {
        const ts = parseTimestamp(trade.timestamp);
        if (!ts) continue;
        const candle = findClosestCandle(this.data, ts);
        if (!candle) continue;

        const isBuy = trade.isBull ?? (trade.side === "B" || trade.side === "BUY");
        const priceVal = isBuy ? candle.low : candle.high;
        const sideText = trade.side === "B" ? "BUY" : "EXIT";
        const optText = trade.strike ? `${trade.strike} ${trade.option_type || ""}`.trim() : (trade.option_type || "");
        const actionText = `${sideText} ${optText}`.trim();

        try {
          const id = this.chart.createOverlay({
            name: "tradeMarker",
            groupId: "trades",
            points: [{ timestamp: candle.time > 1e11 ? candle.time : candle.time * 1000, value: priceVal }],
            lock: true,
            extendData: {
              side: trade.side,
              isBull: isBuy,
              isSpot: true,
              text: actionText,
              qty: trade.quantity,
              price: trade.price,
            },
          });
          if (id) this.tradeOverlayIds.push(id);
        } catch (_) {}
      }
    } else {
      // Option chart:
      // 1. Individual trade markers (Buy / Sell)
      for (const trade of this.tradesData) {
        const ts = parseTimestamp(trade.timestamp);
        const price = Number(trade.price);
        if (!ts || !Number.isFinite(price) || price <= 0) continue;

        try {
          const id = this.chart.createOverlay({
            name: "tradeMarker",
            groupId: "trades",
            points: [{ timestamp: ts, value: price }],
            lock: true,
            extendData: {
              side: trade.side,
              qty: trade.quantity,
              price,
              isSpot: false,
            },
          });
          if (id) this.tradeOverlayIds.push(id);
        } catch (_) {}
      }

      // 2. Connect paired trades (Entry -> Exit) with dashed connector & P&L pill
      const pairs = pairTrades(this.tradesData);
      for (const pair of pairs) {
        const t1 = parseTimestamp(pair.entry.timestamp);
        const p1 = Number(pair.entry.price);
        const t2 = parseTimestamp(pair.exit.timestamp);
        const p2 = Number(pair.exit.price);
        if (!t1 || !t2 || !Number.isFinite(p1) || !Number.isFinite(p2)) continue;

        try {
          const id = this.chart.createOverlay({
            name: "tradeConnector",
            groupId: "trades",
            points: [
              { timestamp: t1, value: p1 },
              { timestamp: t2, value: p2 },
            ],
            lock: true,
            extendData: {
              pnl: pair.pnl,
              pnlPct: pair.pnlPct,
              qty: pair.qty,
            },
          });
          if (id) this.tradeOverlayIds.push(id);
        } catch (_) {}
      }
    }
  }

  refreshPositionOverlay() {
    this.clearPositionOverlay();
    if (!this.showTrades || !this.positionData || !this.chart || !this.data.length) return;

    const pos = this.positionData;
    const qty = Number(pos.quantity || 0);
    const avg = Number(pos.average_price || pos.averagePrice || 0);
    if (!qty || !Number.isFinite(avg) || avg <= 0) return;

    const pnl = Number(pos.unrealised_pnl != null ? pos.unrealised_pnl : (pos.pnl || 0));

    try {
      const id = this.chart.createOverlay({
        name: "positionLine",
        groupId: "position",
        points: [{ value: avg }],
        lock: true,
        extendData: {
          qty,
          avgPrice: avg,
          pnl,
        },
      });
      if (id) this.positionOverlayId = id;
    } catch (_) {}
  }

  update(candle) {
    if (!candle || !Number.isFinite(candle.time) || !this.chart) return;
    const lastIdx = this.data.length - 1;
    const startedNewBar = !(lastIdx >= 0 && this.data[lastIdx].time === candle.time);
    if (!startedNewBar) {
      this.data[lastIdx] = { ...this.data[lastIdx], ...candle };
    } else {
      this.data.push(candle);
      this.refreshDayDividers();
      if (this.showFib) {
        this.refreshFib();
      }
      if (this.showAutoTrendlines) {
        this.refreshAutoTrendlines();
      }
      if (this.showPatterns) {
        this.refreshPatterns();
      }
    }
    const latest = this.data[this.data.length - 1];
    const formatted = this.formatCandle(latest ? { ...latest, ...candle } : candle);
    if (formatted && this.subscriberCallback) {
      this.subscriberCallback(formatted);
    }
    if (this.showDepth) {
      this.debounceDepthRefresh();
    }
    if (this.showVolumeProfile) {
      // A closed bar changes the distribution; intra-bar ticks only move the
      // last candle, so throttle those.
      if (startedNewBar) this.refreshVolumeProfile();
      else this.scheduleVolumeProfile();
    }
  }

  zoomIn() {
    if (this.chart) {
      this.chart.zoomAtCoordinate(1.25);
      if (this.showDepth) this.debounceDepthRefresh();
    }
  }

  zoomOut() {
    if (this.chart) {
      this.chart.zoomAtCoordinate(0.8);
      if (this.showDepth) this.debounceDepthRefresh();
    }
  }

  resetView() {
    if (this.chart) {
      this.resetPriceScale();
      this.chart.scrollToRealTime();
      if (this.showDepth) this.debounceDepthRefresh();
    }
  }

  /** Inclusive data-index window currently rendered by klinecharts. */
  visibleExportRange() {
    const data = this.data || [];
    if (!data.length) return { from: 0, to: -1 };
    let from = 0;
    let to = data.length - 1;
    try {
      const range = this.chart?.getVisibleRange?.();
      if (range && Number.isFinite(range.from) && Number.isFinite(range.to)) {
        from = Math.max(0, Math.floor(range.from));
        to = Math.min(data.length - 1, Math.ceil(range.to));
      }
    } catch (_) {}
    if (to < from) {
      from = Math.max(0, data.length - 120);
      to = data.length - 1;
    }
    return { from, to };
  }

  chartBackgroundColor() {
    try {
      const background = getComputedStyle(this.container).backgroundColor;
      if (background && background !== "transparent" && !/^rgba?\(0,\s*0,\s*0,\s*0\)$/.test(background)) {
        return background;
      }
    } catch (_) {}
    try {
      const themed = getComputedStyle(document.documentElement).getPropertyValue("--chart-bg").trim();
      if (themed) return themed;
    } catch (_) {}
    return this.colors?.bg || "#07090c";
  }

  /**
   * Rasterise every klinecharts canvas in this pane onto one offscreen canvas.
   * The library stacks separate main/overlay canvases per pane (plus the axis
   * panes), so compositing by DOM order reproduces exactly what is on screen.
   */
  async captureChartBlob({ type = "image/png", quality = 0.92 } = {}) {
    if (!this.chart || !this.container) throw new Error("Chart is not ready");
    const box = this.container.getBoundingClientRect();
    if (!(box.width >= 2) || !(box.height >= 2)) throw new Error("Chart is not visible");
    const dpr = Math.max(1, Math.min(3, window.devicePixelRatio || 1));

    // Suppress transient hover artefacts for the capture: crosshair lines/labels
    // and both tooltips (the indicator tooltip has an "always" show rule).
    let previousCrosshair = null;
    let indicatorTooltipRule = "always";
    try {
      previousCrosshair = this.chart.getCrosshair?.() || null;
      indicatorTooltipRule = this.chart.getStyles?.()?.indicator?.tooltip?.showRule || "always";
      this.chart.setStyles({ indicator: { tooltip: { showRule: "none" } } });
      this.chart.setCrosshair({ x: undefined, y: undefined, paneId: undefined, forceInvalidate: true });
    } catch (_) {}

    try {
      // klinecharts redraws on the next animation frame; wait for two so the
      // crosshair removal is painted before sampling the canvases.
      await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));

      const width = Math.max(1, Math.round(box.width * dpr));
      const height = Math.max(1, Math.round(box.height * dpr));
      const output = document.createElement("canvas");
      output.width = width;
      output.height = height;
      const ctx = output.getContext("2d");
      if (!ctx) throw new Error("Canvas 2D context unavailable");

      // JPEG has no alpha: flatten on the themed chart background. Doing the
      // same for PNG keeps exports consistent with the visible pane.
      ctx.fillStyle = this.chartBackgroundColor();
      ctx.fillRect(0, 0, width, height);

      const canvases = Array.from(this.container.querySelectorAll("canvas")) as HTMLCanvasElement[];
      for (const canvas of canvases) {
        const rect = canvas.getBoundingClientRect();
        if (rect.width < 1 || rect.height < 1) continue;
        try {
          const style = getComputedStyle(canvas);
          if (style.display === "none" || style.visibility === "hidden" || Number(style.opacity) === 0) continue;
        } catch (_) {}
        try {
          ctx.drawImage(
            canvas,
            (rect.left - box.left) * dpr,
            (rect.top - box.top) * dpr,
            rect.width * dpr,
            rect.height * dpr,
          );
        } catch (error) {
          console.warn("chart canvas composite skipped", error);
        }
      }

      const mime = type === "image/jpeg" ? "image/jpeg" : "image/png";
      return await encodeCanvas(output, mime, mime === "image/jpeg" ? quality : undefined);
    } finally {
      try {
        this.chart?.setStyles?.({ indicator: { tooltip: { showRule: indicatorTooltipRule } } });
        if (previousCrosshair && typeof previousCrosshair.paneId === "string") {
          this.chart?.setCrosshair?.({
            x: previousCrosshair.x,
            y: previousCrosshair.y,
            paneId: previousCrosshair.paneId,
            forceInvalidate: true,
          });
        }
      } catch (_) {}
    }
  }

  enabledIndicatorNames() {
    const names = [];
    if (this.showVolume) names.push("VOL");
    if (this.showEMA) names.push("EMA(10/20/200)");
    if (this.showVWAP) names.push("VWAP");
    if (this.showRSI) names.push("RSI(14)");
    if (this.showDivergence) names.push("RSI divergence");
    if (this.showMomentum) names.push("Momentum candles");
    if (this.showSupertrend) names.push("Supertrend(10/3)");
    if (this.showATR) names.push("ATR(14)");
    if (this.showOIPane) names.push("OI(contracts)");
    if (this.showOIDeltaPane) names.push("ΔOI(per bar)");
    if (this.showOIWalls && this.oiWalls?.length) names.push("OI walls");
    if (this.showTheta) names.push("Time decay (theta)");
    if (this.showSR) names.push("S/R levels");
    if (this.showAutoTrendlines) names.push("Auto trendlines");
    if (this.showPatterns) names.push("Patterns");
    if (this.showFib) names.push("Fibonacci");
    if (this.showTrades) names.push("Trades/positions");
    if (this.showDepth) names.push("Order-flow DOM");
    if (this.showVolumeProfile) names.push("Volume profile");
    if (this.showMaxPain) names.push("Max pain");
    return names;
  }

  buildIndicatorExportSeries(fromIndex, toIndex) {
    const data = this.data || [];
    const out = [];
    if (!data.length || toIndex < fromIndex) return out;
    const closes = data.map((candle) => Number(candle.close));
    const append = (label, values, digits = 2) => {
      const points = [];
      for (let i = fromIndex; i <= toIndex; i++) {
        const value = values?.[i];
        points.push(Number.isFinite(value) ? Number(value).toFixed(digits) : "-");
      }
      out.push(`${label}: ${points.join(",")}`);
    };

    if (this.showVolume) out.push("vol: in candle CSV volume column");
    if (this.showEMA) {
      for (const period of [10, 20, 200]) {
        append(`ema(${period})`, Indicators.emaSeries(closes, period));
      }
    }
    if (this.showVWAP) append("vwap(session)", exportVwapSeries(data));
    if (this.showRSI) append("rsi(14)", Indicators.rsiSeries(data, 14), 1);
    if (this.showDivergence) {
      const rsi = Indicators.rsiSeries(data, 14);
      const divergences = Indicators.detectDivergences(data, rsi, { lookback: 150, span: 3, minRsiDelta: 1, max: 4 });
      const recent = divergences.slice(-5).map((divergence) =>
        `${divergence.kind}@${formatIstDateTime(divergence.to.time)}(${fmtPrice(divergence.from.price)}→${fmtPrice(divergence.to.price)})`
      );
      out.push(`rsi_divergences: ${recent.length ? recent.join("; ") : "none"}`);
    }
    if (this.showMomentum) {
      const atr = Indicators.atrSeries(data, 14);
      const candles = Indicators.detectMomentumCandles(data, atr, { lookback: 150, multiplier: 1.5, maxBodyRatio: 0.6, max: 40 });
      out.push(`momentum_candles: ${candles.length}`);
    }
    if (this.showSupertrend) {
      const series = supertrendSeries(data, 10, 3);
      append("supertrend_up", series.up);
      append("supertrend_down", series.down);
      out.push(`supertrend_trend: ${series.trend.map((value) => (value === 1 ? "up" : value === -1 ? "down" : "-")).join(",")}`);
    }
    if (this.showATR) append("atr(14)", atrSeries(data, 14));
    if (this.showOIPane && data.some((candle) => Number(candle?.oi) > 0)) {
      append("oi(contracts)", data.map((candle) => Number(candle?.oi) || NaN), 0);
    }
    if (this.showOIDeltaPane) {
      append("oi_delta", oiDeltaSeries(data), 0);
    }
    return out;
  }

  buildChartDerivedLines() {
    const out = [];
    const data = this.data || [];

    if (this.showVWAP && data.length) {
      const vwap = exportVwapSeries(data);
      let lastVwap = NaN;
      for (let i = vwap.length - 1; i >= 0; i--) {
        if (Number.isFinite(vwap[i])) {
          lastVwap = vwap[i];
          break;
        }
      }
      if (Number.isFinite(lastVwap)) out.push(`vwap_last: ${fmtPrice(lastVwap)}`);
    }

    if (this.showMaxPain && this.maxPainValue) {
      const meta = this.maxPainMeta || {};
      const bits = [`max_pain: ${fmtPrice(this.maxPainValue)}`];
      if (Number.isFinite(meta.callOIWall)) bits.push(`call_oi_wall=${fmtCompact(meta.callOIWall)}`);
      if (Number.isFinite(meta.putOIWall)) bits.push(`put_oi_wall=${fmtCompact(meta.putOIWall)}`);
      if (Number.isFinite(meta.pcr)) bits.push(`pcr=${fmtCompact(meta.pcr)}`);
      out.push(bits.join(" | "));
    }

    if (this.showOIWalls && this.oiWalls?.length) {
      out.push(
        `oi_walls: ${this.oiWalls
          .map((wall) => `${wall.side}${wall.metric === "doi" ? "_doi" : "_oi"}=${fmtPrice(wall.strike)}(${fmtCompact(wall.value)})`)
          .join(", ")}`
      );
    }

    if (this.showSR && data.length >= 5) {
      const { supports, resistances } = calculateSRLevels(data);
      if (supports.length) out.push(`support_levels: ${supports.map(fmtPrice).join(",")}`);
      if (resistances.length) out.push(`resistance_levels: ${resistances.map(fmtPrice).join(",")}`);
    }

    if (this.showVolumeProfile) {
      const profile = this.vpSuggestion?.profile || buildVolumeProfile(data);
      if (profile) {
        const nodes = Array.isArray(profile.nodes) ? profile.nodes : [];
        const pocNode = nodes.find((node) => Math.abs(node.price - profile.poc) < 1e-6);
        const buyVsSell = pocNode ? `poc_buy_sell=${fmtCompact(pocNode.buy)}/${fmtCompact(pocNode.sell)}` : "";
        out.push(`volume_profile: poc=${fmtPrice(profile.poc)} vah=${fmtPrice(profile.vah)} val=${fmtPrice(profile.val)} ${buyVsSell}`.trim());
      }
      if (this.vpSuggestion?.buy && this.vpSuggestion?.sell) {
        out.push(
          `volume_suggestion: bias=${this.vpSuggestion.bias || "none"} buy_zone=${fmtPrice(this.vpSuggestion.buy.low)}-${fmtPrice(this.vpSuggestion.buy.high)}`
          + ` sell_zone=${fmtPrice(this.vpSuggestion.sell.low)}-${fmtPrice(this.vpSuggestion.sell.high)}`
          + ` stop=${fmtPrice(this.vpSuggestion.stop)} target=${fmtPrice(this.vpSuggestion.target)}`
        );
      }
    }

    if (this.showAutoTrendlines && data.length) {
      const trendlines = calculateAutoTrendlines(data);
      const support = trendlines?.support?.projectedPrice;
      const resistance = trendlines?.resistance?.projectedPrice;
      if (Number.isFinite(support) || Number.isFinite(resistance)) {
        out.push(`auto_trendline_projection: support=${fmtPrice(support)} resistance=${fmtPrice(resistance)}`);
      }
    }

    if (this.showFib && data.length >= 10) {
      const fib = calculateFibLevels(data);
      if (fib?.levels?.length) {
        out.push(`fibonacci(${fib.direction}): ${fib.levels.map((level) => `${level.ratio}=${fmtPrice(level.price)}`).join(",")}`);
      }
    }

    if (this.showDepth && this.depthData) {
      const range = this.getVisiblePriceRange();
      if (range && range.max > range.min) {
        const step = computeDOMInterval(range.min, range.max, range.height);
        const { buckets } = calculateOrderFlowBuckets(data, this.depthData, this.chainData, range.min, range.max, step);
        const visibleBuckets = (buckets || []).filter((bucket) => bucket.buyVol > 0 || bucket.sellVol > 0);
        if (visibleBuckets.length) {
          let buyVol = 0;
          let sellVol = 0;
          for (const bucket of visibleBuckets) {
            buyVol += bucket.buyVol;
            sellVol += bucket.sellVol;
          }
          const top = [...visibleBuckets]
            .sort((a, b) => (b.buyVol + b.sellVol) - (a.buyVol + a.sellVol))
            .slice(0, 3)
            .map((bucket) => `${fmtPrice(bucket.price)}:${fmtCompact(bucket.buyVol)}/${fmtCompact(bucket.sellVol)}`);
          out.push(`depth_visible(step=${fmtCompact(step)}): buy_vol=${fmtCompact(buyVol)} sell_vol=${fmtCompact(sellVol)} top_price:buy/sell=${top.join(";")}`);
        }
      }
    }

    if (this.showTrades && this.tradesData?.length) {
      out.push(`trade_markers: ${this.tradesData.length}`);
    }

    if (this.showTheta && data.length) {
      const last = data[data.length - 1];
      const bits = [];
      if (Number.isFinite(last.tdCE)) bits.push(`call=${fmtPrice(last.tdCE)}`);
      if (Number.isFinite(last.tdPE)) bits.push(`put=${fmtPrice(last.tdPE)}`);
      if (bits.length) out.push(`theta_last: ${bits.join(" ")}`);
    }

    return out;
  }

  buildChartSummary(visible, first, last) {
    if (!visible.length || !first || !last) return "no visible candles";
    const parts = [];
    const changePct = first.close > 0 ? ((last.close - first.close) / first.close) * 100 : 0;
    const direction = changePct > 0.15 ? "uptrend" : changePct < -0.15 ? "downtrend" : "sideways";
    parts.push(`${direction} (${changePct >= 0 ? "+" : ""}${changePct.toFixed(2)}% over ${visible.length} bars)`);

    const closes = (this.data || []).map((candle) => Number(candle.close));
    if (closes.length) {
      const ema20 = Indicators.emaSeries(closes, 20);
      const ema200 = Indicators.emaSeries(closes, 200);
      const last20 = ema20[ema20.length - 1];
      const last200 = ema200[ema200.length - 1];
      if (Number.isFinite(last20) && Number.isFinite(last200)) {
        parts.push(last20 >= last200 ? "EMA20 above EMA200" : "EMA20 below EMA200");
      }
    }

    if (this.showVWAP) {
      const vwap = exportVwapSeries(this.data);
      let lastVwap = NaN;
      for (let i = vwap.length - 1; i >= 0; i--) {
        if (Number.isFinite(vwap[i])) {
          lastVwap = vwap[i];
          break;
        }
      }
      if (Number.isFinite(lastVwap)) {
        parts.push(last.close >= lastVwap ? "price above VWAP" : "price below VWAP");
      }
    }

    if (this.showOIPane || this.showOIDeltaPane) {
      const latestOI = this.latestOI();
      const deltas = oiDeltaSeries(this.data);
      let lastDelta = NaN;
      for (let i = deltas.length - 1; i >= 0; i--) {
        if (Number.isFinite(deltas[i])) {
          lastDelta = deltas[i];
          break;
        }
      }
      if (latestOI > 0) {
        const deltaText = Number.isFinite(lastDelta)
          ? ` (last bar ΔOI ${lastDelta >= 0 ? "+" : ""}${fmtCompact(lastDelta)})`
          : "";
        parts.push(`OI ${fmtCompact(latestOI)}${deltaText}`);
      }
    }

    const tail = visible.slice(-10);
    if (tail.length >= 4) {
      const half = Math.floor(tail.length / 2);
      const avg = (rows) => rows.reduce((sum, row) => sum + Number(row.volume || 0), 0) / Math.max(1, rows.length);
      const recentVolume = avg(tail.slice(-half));
      const priorVolume = avg(tail.slice(0, half));
      if (recentVolume > 0 && priorVolume > 0) {
        const ratio = recentVolume / priorVolume;
        parts.push(ratio > 1.15 ? "volume expanding" : ratio < 0.85 ? "volume declining" : "volume steady");
      }
    }

    if (this.showRSI) {
      const rsi = Indicators.rsiSeries(this.data, 14);
      const lastRsi = rsi[rsi.length - 1];
      if (Number.isFinite(lastRsi)) {
        const zone = lastRsi >= 70 ? "overbought" : lastRsi <= 30 ? "oversold" : "neutral";
        parts.push(`RSI(14) ${lastRsi.toFixed(1)} ${zone}`);
      }
    }

    return parts.join(", ");
  }

  /**
   * Token-efficient Markdown report meant to be pasted into an LLM chat.
   * One CSV row per candle, no nested/pretty-printed JSON.
   */
  buildLLMExport(meta: any = {}) {
    const data = this.data || [];
    const { from, to } = this.visibleExportRange();
    const visible = to >= from ? data.slice(from, to + 1) : [];
    const first = visible[0] || data[0] || null;
    const last = visible[visible.length - 1] || data[data.length - 1] || null;
    const paneLabel = meta.paneLabel || meta.pane || "Chart";
    const symbol = meta.symbol || this.currentSymbol || "Chart";
    const timeframe = meta.timeframe || "—";
    const lines = [];

    lines.push(`# Chart export · ${symbol} · ${timeframe} · ${paneLabel}`);
    lines.push("");
    lines.push(`symbol: ${symbol}`);
    lines.push(`index: ${meta.index || "—"}`);
    lines.push(`expiry: ${meta.expiry || "—"}`);
    lines.push(`timeframe: ${timeframe}`);
    lines.push(`pane: ${paneLabel}`);
    lines.push(`pane_key: ${meta.pane || "—"}`);
    lines.push(`theme: ${meta.theme || "dark"}`);
    lines.push(`generated_at: ${formatIstNow()}`);
    if (first && last) {
      lines.push(`visible_range_ist: ${formatIstDateTime(first.time)} → ${formatIstDateTime(last.time)} (${visible.length} bars)`);
      lines.push(`current_price: ${fmtPrice(last.close)}`);
    } else {
      lines.push("visible_range_ist: —");
      lines.push("current_price: —");
    }
    lines.push(`overlays: ${this.enabledIndicatorNames().join(", ") || "none"}`);
    lines.push(`summary: ${this.buildChartSummary(visible, first, last)}`);
    lines.push("");

    lines.push("## Candles (visible, time IST)");
    lines.push("```csv");
    const visibleHasOI = visible.some((candle) => Number(candle?.oi) > 0);
    lines.push(`time_ist,open,high,low,close,volume${visibleHasOI ? ",oi" : ""}`);
    for (const candle of visible) {
      const oiCell = Number(candle?.oi) > 0 ? String(Math.round(Number(candle.oi))) : "-";
      lines.push(
        `${formatIstDateTime(candle.time)},${fmtPrice(candle.open)},${fmtPrice(candle.high)},`
        + `${fmtPrice(candle.low)},${fmtPrice(candle.close)},${fmtCompact(candle.volume)}`
        + `${visibleHasOI ? `,${oiCell}` : ""}`
      );
    }
    lines.push("```");
    lines.push("");

    lines.push("## Indicators");
    const tail = Math.min(20, visible.length);
    if (tail > 0) {
      lines.push(`indicator_tail_bars: ${tail} (oldest→newest, aligned to the last ${tail} CSV rows)`);
      const series = this.buildIndicatorExportSeries(to - tail + 1, to);
      lines.push(...(series.length ? series : ["none enabled"]));
    } else {
      lines.push("none enabled");
    }
    lines.push("");

    lines.push("## Derived / overlays");
    const derived = this.buildChartDerivedLines();
    lines.push(...(derived.length ? derived : ["none enabled"]));
    lines.push("");

    return lines.join("\n");
  }

  applyTheme() {
    this.colors = chartColors(this.container);
    if (this.chart) {
      this.chart.setStyles(buildStyles(this.colors));
    }
    // setStyles resets the tooltip rule, so re-apply the pane density.
    this.syncChartDensity(true);
    this.refreshSRLines();
    this.refreshFib();
    this.refreshMaxPain();
    this.refreshOIWalls();
    this.refreshAutoTrendlines();
    this.refreshPatterns();
    this.refreshDepth();
  }

  /**
   * Keep indicator read-outs off the candles: the always-on VWAP/VOL legend is
   * painted in the plot's top-left corner, where it collides with the
   * session-high marker on phone-sized panes. The axis last-value marks still
   * carry the numbers there, so the legend is dropped below the same 440px
   * narrow-pane threshold used by the DOM ladder.
   */
  syncChartDensity(force = false) {
    if (!this.chart || !this.container) return;
    const width = paneWidth(this.container);
    if (!width) return;
    const narrow = width < NARROW_PANE_WIDTH;
    if (!force && this.compactPane === narrow) return;
    this.compactPane = narrow;
    try {
      this.chart.setStyles({ indicator: { tooltip: { showRule: narrow ? "none" : "always" } } });
    } catch (error) {
      console.warn("chart density sync failed", error);
    }
  }

  /**
   * klinecharts gives every indicator pane its requested height (100px by
   * default) and hands whatever is left to the candle pane. On a short
   * phone/landscape container that leaves the price pane at 0-22px, so budget
   * the stack here: the candle pane keeps at least CANDLE_MIN_HEIGHT, indicator
   * panes shrink up to INDICATOR_MAX_HEIGHT_RATIO of the container and are
   * hidden when they would be unreadably small. Natural heights are restored
   * as soon as the container is tall enough again.
   */
  syncPaneHeights() {
    if (!this.chart || !this.container) return;
    const containerHeight = Number(this.container.clientHeight) || 0;
    // Hidden pane (mobile view switched away, display:none): leave the sizes
    // to the next visible resize instead of writing a zero-height stack.
    if (!(containerHeight > 0)) return;

    let panes = [];
    try {
      panes = this.chart.getDrawPanes?.() || [];
    } catch (_) {
      return;
    }
    if (!panes.length) return;
    const xAxisPane = this.chart.getXAxisPane?.() || null;
    const xAxisId = xAxisPane?.getId?.() || "x_axis_pane";
    const candlePane = panes.find((pane) => pane?.getId?.() === "candle_pane");
    if (!candlePane) return;
    const indicatorPanes = panes.filter((pane) => {
      const id = pane?.getId?.();
      return Boolean(id) && pane !== xAxisPane && id !== "candle_pane" && id !== xAxisId;
    });
    if (!indicatorPanes.length) return;
    const states = [candlePane, ...indicatorPanes].map((pane) => pane.getOptions?.().state || "normal");
    // A maximised pane already owns the whole container; do not fight it.
    if (states.some((state) => state !== "normal")) return;

    const xAxisHeight = Number(xAxisPane?.getBounding?.().height) || DEFAULT_X_AXIS_HEIGHT;
    let separatorSize = 1;
    try {
      const size = Number(this.chart.getStyles?.().separator?.size);
      if (Number.isFinite(size) && size >= 0) separatorSize = size;
    } catch (_) {}
    const separators = indicatorPanes.length * separatorSize;
    const usable = Math.max(0, containerHeight - xAxisHeight - separators);

    const active = new Set();
    const entries = indicatorPanes.map((pane) => {
      const id = pane.getId();
      active.add(id);
      const options = pane.getOptions?.() || {};
      const optionHeight = Number(options.height);
      const boundingHeight = Number(pane.getBounding?.().height);
      let record = this.paneSizing.get(id);
      // Indicator panes are recreated when an indicator is toggled off/on
      // (`pane_rsi` keeps its id), so identity matters more than the id.
      if (!record || record.pane !== pane) {
        record = {
          pane,
          base: Number.isFinite(optionHeight) && optionHeight > 0 ? optionHeight : DEFAULT_INDICATOR_PANE_HEIGHT,
          applied: Number.isFinite(boundingHeight) && boundingHeight > 0 ? boundingHeight : null,
          minHeight: Number.isFinite(Number(options.minHeight)) ? Number(options.minHeight) : LIBRARY_PANE_MIN_HEIGHT,
        };
      } else if (Number.isFinite(optionHeight) && optionHeight > 0 && optionHeight !== record.applied) {
        // The user dragged this separator (or the library changed the pane):
        // adopt the new size as its natural height so a later restore keeps it.
        record.base = optionHeight;
      }
      this.paneSizing.set(id, record);
      return { pane, id, record, base: Math.max(1, record.base) };
    });
    for (const id of [...this.paneSizing.keys()]) {
      if (!active.has(id)) this.paneSizing.delete(id);
    }

    const naturalTotal = entries.reduce((sum, entry) => sum + entry.base, 0);
    // While the stack fits and the candle pane keeps its minimum, leave the
    // natural heights untouched so wide/desktop geometry does not change.
    const shrink = usable - naturalTotal < CANDLE_MIN_HEIGHT;
    let budget = naturalTotal;
    if (shrink) {
      // The stack does not fit: cap the indicator share and leave the candle
      // pane its minimum. A pane that cannot reach a usable height is hidden,
      // not squeezed, so leftover budget flows to the panes that can.
      budget = Math.min(
        naturalTotal,
        Math.floor(containerHeight * INDICATOR_MAX_HEIGHT_RATIO),
        Math.max(0, usable - CANDLE_MIN_HEIGHT)
      );
    }

    let remaining = Math.max(0, budget);
    let changed = false;
    for (const entry of entries) {
      let target = Math.min(entry.base, remaining);
      if (shrink && target < INDICATOR_PANE_MIN_HEIGHT) target = 0;
      remaining = Math.max(0, remaining - target);
      const minHeight = target > 0 ? LIBRARY_PANE_MIN_HEIGHT : 0;
      const currentHeight = Number(entry.pane.getBounding?.().height);
      // The pane may have been re-created (record reset) or relaid out by the
      // library while its container was hidden, so trust the real bounding box
      // over the last value we wrote.
      const settled = Number.isFinite(currentHeight) && Math.abs(currentHeight - target) <= 0.5;
      if (entry.record.applied === target && entry.record.minHeight === minHeight && settled) continue;
      try {
        this.chart.setPaneOptions({ id: entry.id, height: target, minHeight });
      } catch (error) {
        console.warn("chart pane sizing failed", error);
        return;
      }
      entry.record.applied = target;
      entry.record.minHeight = minHeight;
      changed = true;
    }
    if (!changed) return;
    // `setPaneOptions` only triggers a layout for height > 0, and a hidden
    // pane's height is 0, so ask for one explicit layout for the whole stack.
    try {
      this.chart.layout?.({
        measureHeight: true,
        measureWidth: true,
        update: true,
        buildYAxisTick: true,
        forceBuildYAxisTick: true,
      });
    } catch (error) {
      console.warn("chart pane layout failed", error);
    }
  }

  /** True when the sizing pass gave this pane a visible height (or has not run yet). */
  paneIsUsable(paneId) {
    const record = this.paneSizing?.get(paneId);
    if (!record) return true;
    return Number(record.applied) > 0;
  }

  /** Re-allocate the canvas backing stores when the device pixel ratio changes. */
  watchPixelRatio() {
    this.disposePixelRatioWatch();
    if (typeof window === "undefined") return;
    const onChange = () => {
      // Re-arm for the new ratio, then resize on the next frame so the layout
      // has already settled at the new device pixel size.
      this.watchPixelRatio();
      if (typeof window.requestAnimationFrame === "function") {
        window.requestAnimationFrame(() => this.resize());
      } else {
        this.resize();
      }
    };
    let query = null;
    if (typeof window.matchMedia === "function") {
      // The query must use the raw ratio: a 3.75dppx phone never matches a
      // `(resolution: 3dppx)` query built from the capped bitmap ratio.
      query = window.matchMedia(`(resolution: ${rawDevicePixelRatio()}dppx)`);
      try {
        if (typeof query.addEventListener === "function") query.addEventListener("change", onChange);
        else if (typeof query.addListener === "function") query.addListener(onChange);
      } catch (_) {
        query = null;
      }
    }
    // Media queries miss a DPR change that lands while the page is idle (and
    // every emulated viewport), so poll the cheap comparison as a safety net.
    const timer = window.setInterval(() => {
      if (typeof document !== "undefined" && document.visibilityState === "hidden") return;
      if (this.pixelRatioTimerRatio !== canvasPixelRatio()) {
        this.pixelRatioTimerRatio = canvasPixelRatio();
        this.resize();
      }
    }, 1000);
    this.pixelRatioTimerRatio = canvasPixelRatio();
    this.pixelRatioQuery = { query, onChange, timer };
  }

  disposePixelRatioWatch() {
    const current = this.pixelRatioQuery;
    if (!current) return;
    this.pixelRatioQuery = null;
    try {
      if (current.timer) window.clearInterval(current.timer);
      if (current.query?.removeEventListener) current.query.removeEventListener("change", current.onChange);
      else if (current.query?.removeListener) current.query.removeListener(current.onChange);
    } catch (_) {}
  }

  /**
   * Re-read CSS box × pixel ratio for every canvas, but only when the effective
   * ratio actually changed: `chart.resize()` already re-allocates the bitmaps
   * of panes whose box changed, so forcing every frame would reallocate all
   * canvases on each resize for nothing.
   */
  resizeCanvases() {
    const ratio = canvasPixelRatio();
    const ratioChanged = this.canvasRatio !== ratio;
    this.canvasRatio = ratio;
    if (!ratioChanged) return;
    for (const canvas of chartCanvasWidgets(this.chart)) {
      try {
        canvas._scheduleDraw?.(true);
      } catch (_) {}
    }
  }

  schedule() {
    this.syncChartDensity();
    if (this.chart) {
      this.chart.resize();
      this.syncPaneHeights();
      this.resizeCanvases();
    }
  }

  resize() {
    this.syncChartDensity();
    if (this.chart) {
      this.chart.resize();
      this.syncPaneHeights();
      this.resizeCanvases();
    }
  }

  destroy() {
    this.disposePixelRatioWatch();
    this.paneSizing?.clear();
    if (this._depthTimer) {
      clearTimeout(this._depthTimer);
      this._depthTimer = null;
    }
    if (this._vpTimer) {
      clearTimeout(this._vpTimer);
      this._vpTimer = null;
    }
    this.levelDrag = null;
    this.clearLevelDragPreview();
    if (this.container && this.levelPointerHandlers) {
      this.container.removeEventListener("pointerdown", this.levelPointerHandlers.down, true);
      this.container.removeEventListener("pointermove", this.levelPointerHandlers.move, true);
      this.container.removeEventListener("pointerup", this.levelPointerHandlers.up, true);
      this.container.removeEventListener("pointercancel", this.levelPointerHandlers.up, true);
      this.levelPointerHandlers = null;
    }
    this.draftLevels = { entry: 0, target: 0, stop: 0, side: "B" };
    this.clearDraftOverlays();
    this.clearOIWalls();
    this.clearOpenOrders();
    this.clearTradeOverlays();
    this.clearPositionOverlay();
    this.clearBracketLevels();
    this.clearSRLines();
    this.clearDayDividers();
    this.clearAutoTrendlines();
    this.clearDepth();
    this.clearPatterns();
    if (this.chart && window.klinecharts) {
      window.klinecharts.dispose(this.chart);
    }
  }
}
