// Minimal dependency-free candlestick chart. The plan calls for TradingView
// Lightweight Charts; this canvas renderer keeps the single-binary, no-CDN
// constraint while preserving the intended dark terminal look. It can be
// swapped for the vendored library without changing app state.

const COLORS = {
  bg: "#07090c",
  grid: "#141c24",
  text: "#8292a2",
  crosshair: "#5d6b79",
  up: "#35c46a",
  down: "#ff5d5d",
  last: "#f0b429",
  volumeUp: "#35c46a44",
  volumeDown: "#ff5d5d44",
};

function fmt(n, digits = 2) {
  if (!Number.isFinite(n)) return "—";
  return n.toLocaleString("en-IN", { minimumFractionDigits: digits, maximumFractionDigits: digits });
}

export class CandleChart {
  constructor(canvas, { showVolume = true } = {}) {
    this.canvas = canvas;
    this.ctx = canvas.getContext("2d");
    this.data = [];
    this.showVolume = showVolume;
    this.hover = null;
    this.lastWidth = 0;
    this.lastHeight = 0;
    this.raf = 0;
    // null = auto-fit the latest bars. { count, offset } lets the user zoom and
    // pan, where offset is the number of bars between the right edge and the
    // newest candle.
    this.view = null;
    this.drag = null;
    this.priceZoom = 1;
    canvas.title = "Scroll to zoom time + price · Shift+scroll for price · drag to pan · double-click to reset";
    canvas.setAttribute("aria-label", "Candlestick chart. Scroll to zoom time and price, drag to pan, double-click to reset.");

    this.onMove = (event) => {
      const rect = this.canvas.getBoundingClientRect();
      this.hover = { x: event.clientX - rect.left, y: event.clientY - rect.top };
      this.schedule();
    };
    this.onLeave = () => {
      if (this.drag) return;
      this.hover = null;
      this.schedule();
    };
    this.onWheel = (event) => {
      if (!this.data.length) return;
      event.preventDefault();
      const rect = this.canvas.getBoundingClientRect();
      const x = event.clientX - rect.left;
      const { width, right } = this.viewGeometry();
      const overPriceAxis = x >= width - right;
      const priceOnly = event.shiftKey || overPriceAxis;
      const timeOnly = event.altKey && !event.shiftKey && !overPriceAxis;
      this.zoomBy(event.deltaY > 0 ? 1.18 : 1 / 1.18, x, {
        time: !priceOnly,
        price: !timeOnly,
      });
    };
    this.onPointerDown = (event) => {
      if (event.button !== 0 || !this.data.length) return;
      event.preventDefault();
      const count = this.visibleCount();
      this.drag = { x: event.clientX, offset: this.currentOffset(count) };
      this.canvas.classList.add("dragging");
      this.canvas.setPointerCapture?.(event.pointerId);
    };
    this.onPointerMove = (event) => {
      if (!this.drag) return;
      const count = this.visibleCount();
      const { plotW } = this.viewGeometry();
      const barSlot = plotW / count;
      const offset = this.drag.offset + (event.clientX - this.drag.x) / Math.max(1, barSlot);
      this.setOffset(offset, count);
    };
    this.onPointerUp = (event) => {
      if (!this.drag) return;
      this.drag = null;
      this.canvas.classList.remove("dragging");
      this.canvas.releasePointerCapture?.(event.pointerId);
      this.schedule();
    };
    this.onDoubleClick = () => this.resetView();

    canvas.addEventListener("mousemove", this.onMove);
    canvas.addEventListener("mouseleave", this.onLeave);
    canvas.addEventListener("wheel", this.onWheel, { passive: false });
    canvas.addEventListener("pointerdown", this.onPointerDown);
    canvas.addEventListener("pointermove", this.onPointerMove);
    canvas.addEventListener("pointerup", this.onPointerUp);
    canvas.addEventListener("pointercancel", this.onPointerUp);
    canvas.addEventListener("dblclick", this.onDoubleClick);

    if (typeof ResizeObserver !== "undefined") {
      this.observer = new ResizeObserver(() => this.schedule());
      this.observer.observe(canvas.parentElement || canvas);
    }
  }

  setData(data) {
    this.data = Array.isArray(data) ? data.filter((c) => Number.isFinite(c?.close)) : [];
    // Keep an explicit zoom/pan when live candles are refreshed. The range is
    // clamped to the new data length during render.
    this.schedule();
  }

  viewGeometry() {
    const width = this.canvas.clientWidth || 320;
    const height = this.canvas.clientHeight || 180;
    const right = 50;
    const left = 4;
    const plotW = Math.max(10, width - left - right);
    return { width, height, left, right, plotW };
  }

  visibleCount() {
    const { plotW } = this.viewGeometry();
    const total = this.data.length;
    if (!total) return 1;
    // The default shows roughly the latest 70 candles, regardless of pane width.
    const auto = Math.max(12, Math.min(total, Math.floor(plotW / 6)));
    if (!this.view) return auto;
    return Math.max(Math.min(8, total), Math.min(total, Math.round(this.view.count)));
  }

  currentOffset(count = this.visibleCount()) {
    const max = Math.max(0, this.data.length - count);
    return Math.max(0, Math.min(max, Math.round(this.view?.offset || 0)));
  }

  setOffset(offset, count = this.visibleCount()) {
    const max = Math.max(0, this.data.length - count);
    this.view = { count, offset: Math.max(0, Math.min(max, Math.round(offset))) };
    this.schedule();
  }

  zoomBy(factor, anchorX, { time = true, price = true } = {}) {
    const total = this.data.length;
    if (!total) return;
    if (time) {
      const count = this.visibleCount();
      const offset = this.currentOffset(count);
      const { left, plotW } = this.viewGeometry();
      const ratio = Math.max(0, Math.min(1, (anchorX - left) / plotW));
      const start = Math.max(0, total - offset - count);
      const anchorIndex = start + ratio * count;
      const nextCount = Math.max(Math.min(8, total), Math.min(total, Math.round(count * factor)));
      if (nextCount !== count) {
        let nextStart = Math.round(anchorIndex - ratio * nextCount);
        nextStart = Math.max(0, Math.min(total - nextCount, nextStart));
        this.view = { count: nextCount, offset: total - nextStart - nextCount };
      }
    }
    if (price) {
      // Keep the current price centred while the y-range expands/contracts.
      this.priceZoom = Math.max(0.2, Math.min(24, this.priceZoom / factor));
    }
    this.schedule();
  }

  zoomIn() {
    const { left, plotW } = this.viewGeometry();
    this.zoomBy(0.72, left + plotW / 2);
  }

  zoomOut() {
    const { left, plotW } = this.viewGeometry();
    this.zoomBy(1.38, left + plotW / 2);
  }

  resetView() {
    this.view = null;
    this.priceZoom = 1;
    this.schedule();
  }

  schedule() {
    if (this.raf) return;
    this.raf = requestAnimationFrame(() => {
      this.raf = 0;
      this.render();
    });
  }

  layout() {
    const dpr = window.devicePixelRatio || 1;
    const width = this.canvas.clientWidth || 320;
    const height = this.canvas.clientHeight || 180;
    if (this.lastWidth !== width || this.lastHeight !== height) {
      this.lastWidth = width;
      this.lastHeight = height;
      this.canvas.width = Math.round(width * dpr);
      this.canvas.height = Math.round(height * dpr);
    }
    this.ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    return { width, height, dpr };
  }

  render() {
    const { width, height } = this.layout();
    const ctx = this.ctx;
    ctx.clearRect(0, 0, width, height);
    ctx.fillStyle = COLORS.bg;
    ctx.fillRect(0, 0, width, height);

    if (!this.data.length) return;

    const right = 50;
    const left = 4;
    const top = 6;
    const bottom = 16;
    const volumeH = this.showVolume ? Math.min(46, (height - top - bottom) * 0.24) : 0;
    const plotH = height - top - bottom - volumeH;
    const plotW = Math.max(10, width - left - right);

    const count = this.visibleCount();
    const offset = this.currentOffset(count);
    const start = Math.max(0, this.data.length - offset - count);
    const data = this.data.slice(start, start + count);
    const barSlot = plotW / Math.max(1, data.length);

    let min = Infinity;
    let max = -Infinity;
    let maxVol = 0;
    for (const c of data) {
      min = Math.min(min, c.low);
      max = Math.max(max, c.high);
      maxVol = Math.max(maxVol, c.volume || 0);
    }
    if (!Number.isFinite(min) || !Number.isFinite(max)) return;
    const pad = Math.max((max - min) * 0.08, max * 0.001, 0.01);
    min -= pad;
    max += pad;
    if (this.priceZoom !== 1) {
      const anchor = data[data.length - 1].close;
      const span = Math.max((max - min) / this.priceZoom, Math.max(0.01, Math.abs(anchor) * 0.0005));
      min = anchor - span / 2;
      max = anchor + span / 2;
    }
    const range = Math.max(max - min, 1e-6);
    const yOf = (price) => top + ((max - price) / range) * plotH;

    // Horizontal grid + price labels.
    ctx.font = "9px ui-monospace, SFMono-Regular, Menlo, monospace";
    ctx.fillStyle = COLORS.text;
    ctx.textAlign = "left";
    ctx.textBaseline = "middle";
    const gridLines = 4;
    for (let i = 0; i <= gridLines; i++) {
      const y = top + (plotH / gridLines) * i;
      ctx.strokeStyle = COLORS.grid;
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(left, Math.round(y) + 0.5);
      ctx.lineTo(width - right, Math.round(y) + 0.5);
      ctx.stroke();
      const price = max - (range / gridLines) * i;
      ctx.fillText(fmt(price), width - right + 5, y);
    }

    // Volume separators.
    const volumeTop = top + plotH + 4;
    if (this.showVolume) {
      ctx.strokeStyle = COLORS.grid;
      ctx.beginPath();
      ctx.moveTo(left, volumeTop - 2.5);
      ctx.lineTo(width - right, volumeTop - 2.5);
      ctx.stroke();
    }

    // Candles.
    data.forEach((c, i) => {
      const x = left + i * barSlot + barSlot / 2;
      const up = c.close >= c.open;
      const color = up ? COLORS.up : COLORS.down;
      const bodyW = Math.max(1, Math.min(barSlot - 2, 14));
      const yHigh = yOf(c.high);
      const yLow = yOf(c.low);
      const yOpen = yOf(c.open);
      const yClose = yOf(c.close);

      ctx.strokeStyle = color;
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(Math.round(x) + 0.5, yHigh);
      ctx.lineTo(Math.round(x) + 0.5, yLow);
      ctx.stroke();

      const bodyTop = Math.min(yOpen, yClose);
      const bodyH = Math.max(1, Math.abs(yClose - yOpen));
      ctx.fillStyle = color;
      ctx.fillRect(Math.round(x - bodyW / 2), Math.round(bodyTop), bodyW, Math.round(bodyH));

      if (this.showVolume && maxVol > 0) {
        const vh = ((c.volume || 0) / maxVol) * Math.max(2, volumeH - 4);
        ctx.fillStyle = up ? COLORS.volumeUp : COLORS.volumeDown;
        ctx.fillRect(Math.round(x - bodyW / 2), volumeTop + (volumeH - vh), bodyW, Math.max(1, vh));
      }
    });

    // Last price marker.
    const last = data[data.length - 1];
    const yLast = yOf(last.close);
    ctx.strokeStyle = COLORS.last;
    ctx.setLineDash([3, 3]);
    ctx.beginPath();
    ctx.moveTo(left, Math.round(yLast) + 0.5);
    ctx.lineTo(width - right, Math.round(yLast) + 0.5);
    ctx.stroke();
    ctx.setLineDash([]);

    const label = fmt(last.close);
    ctx.font = "9px ui-monospace, SFMono-Regular, Menlo, monospace";
    const labelW = ctx.measureText(label).width + 8;
    ctx.fillStyle = "#3a2c08";
    ctx.fillRect(width - right + 2, yLast - 7, Math.min(labelW, right - 4), 14);
    ctx.fillStyle = COLORS.last;
    ctx.fillText(label, width - right + 6, yLast);

    // Time labels.
    ctx.fillStyle = COLORS.text;
    ctx.textAlign = "center";
    ctx.textBaseline = "top";
    const labelCount = Math.min(5, data.length);
    for (let i = 0; i < labelCount; i++) {
      const idx = Math.floor((data.length - 1) * (i / Math.max(1, labelCount - 1)));
      const c = data[idx];
      const x = left + idx * barSlot + barSlot / 2;
      const d = new Date(c.time * 1000);
      const text = d.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit", timeZone: "Asia/Kolkata" });
      ctx.fillText(text, Math.min(width - right - 12, Math.max(left + 14, x)), height - bottom + 4);
    }

    // Crosshair + OHLC legend.
    if (this.hover && this.hover.x >= left && this.hover.x <= width - right && this.hover.y <= top + plotH) {
      const idx = Math.max(0, Math.min(data.length - 1, Math.floor((this.hover.x - left) / barSlot)));
      const c = data[idx];
      const x = left + idx * barSlot + barSlot / 2;
      const y = this.hover.y;

      ctx.strokeStyle = COLORS.crosshair;
      ctx.setLineDash([2, 3]);
      ctx.beginPath();
      ctx.moveTo(Math.round(x) + 0.5, top);
      ctx.lineTo(Math.round(x) + 0.5, top + plotH);
      ctx.moveTo(left, Math.round(y) + 0.5);
      ctx.lineTo(width - right, Math.round(y) + 0.5);
      ctx.stroke();
      ctx.setLineDash([]);

      const price = max - ((y - top) / plotH) * range;
      ctx.fillStyle = "#1a232c";
      ctx.fillRect(width - right + 2, y - 7, right - 4, 14);
      ctx.fillStyle = "#d4dee7";
      ctx.textAlign = "left";
      ctx.textBaseline = "middle";
      ctx.fillText(fmt(price), width - right + 5, y);

      const legend = `O ${fmt(c.open)}  H ${fmt(c.high)}  L ${fmt(c.low)}  C ${fmt(c.close)}`;
      ctx.font = "9px ui-monospace, SFMono-Regular, Menlo, monospace";
      const legendW = ctx.measureText(legend).width + 12;
      ctx.fillStyle = "#0d1319dd";
      ctx.fillRect(left + 3, top + 2, legendW, 15);
      ctx.fillStyle = c.close >= c.open ? COLORS.up : COLORS.down;
      ctx.textBaseline = "middle";
      ctx.fillText(legend, left + 9, top + 10);
    }

    // Keep the crosshair index in sync with rendered slice for future use.
    this.visibleStart = start;
  }

  destroy() {
    this.canvas.removeEventListener("mousemove", this.onMove);
    this.canvas.removeEventListener("mouseleave", this.onLeave);
    this.canvas.removeEventListener("wheel", this.onWheel);
    this.canvas.removeEventListener("pointerdown", this.onPointerDown);
    this.canvas.removeEventListener("pointermove", this.onPointerMove);
    this.canvas.removeEventListener("pointerup", this.onPointerUp);
    this.canvas.removeEventListener("pointercancel", this.onPointerUp);
    this.canvas.removeEventListener("dblclick", this.onDoubleClick);
    this.observer?.disconnect();
  }
}
