import { CandlestickSeries, HistogramSeries, LineStyle, createChart } from "./lightweight-charts.standalone.production.js";

const chartColors = (element) => {
  const styles = getComputedStyle(element);
  const color = (name, fallback) => styles.getPropertyValue(name).trim() || fallback;
  return {
    bg: color("--chart-bg", "#07090c"),
    grid: color("--chart-grid", "#141c24"),
    text: color("--chart-text", "#8292a2"),
    up: color("--green", "#35c46a"),
    down: color("--red", "#ff5d5d"),
    border: color("--line", "#24303a"),
    resistance: color("--chart-res", "#f05a5a"),
    support: color("--chart-sup", "#29b360"),
  };
};

const formatVolume = (val) => {
  if (!Number.isFinite(val) || val <= 0) return "0";
  if (val >= 1e7) return `${(val / 1e7).toFixed(2)}Cr`;
  if (val >= 1e5) return `${(val / 1e5).toFixed(2)}L`;
  if (val >= 1e3) return `${(val / 1e3).toFixed(1)}K`;
  return String(Math.round(val));
};

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

const axisTime = (time) => new Date(Number(time) * 1000).toLocaleTimeString("en-GB", {
  hour: "2-digit", minute: "2-digit", timeZone: "Asia/Kolkata",
});
const istDateFormatter = new Intl.DateTimeFormat("en-US", {
  timeZone: "Asia/Kolkata", year: "numeric", month: "2-digit", day: "2-digit",
});
const istDayKey = (time) => {
  const parts = Object.fromEntries(istDateFormatter.formatToParts(new Date(time * 1000)).map((part) => [part.type, part.value]));
  return `${parts.year}-${parts.month}-${parts.day}`;
};

export class CandleChart {
  constructor(container, { showVolume = true, showSR = false } = {}) {
    this.container = container;
    this.data = [];
    this.showSR = showSR;
    this.srPriceLines = [];
    this.colors = chartColors(container);
    this.chart = createChart(container, {
      autoSize: true,
      width: container.clientWidth || 320,
      height: container.clientHeight || 240,
      layout: {
        background: { type: "solid", color: this.colors.bg },
        textColor: this.colors.text,
        attributionLogo: true,
      },
      localization: {
        locale: "en-IN",
        priceFormatter: (price) => Number(price).toLocaleString("en-IN", {
          minimumFractionDigits: 2, maximumFractionDigits: 2,
        }),
      },
      grid: {
        vertLines: { color: this.colors.grid },
        horzLines: { color: this.colors.grid },
      },
      rightPriceScale: { borderColor: this.colors.border, minimumWidth: 58 },
      timeScale: {
        borderColor: this.colors.border,
        timeVisible: true,
        secondsVisible: false,
        rightOffset: 4,
        tickMarkFormatter: (time) => axisTime(time),
      },
      crosshair: { vertLine: { color: this.colors.text }, horzLine: { color: this.colors.text } },
      handleScroll: { mouseWheel: false, pressedMouseMove: true, horzTouchDrag: true, vertTouchDrag: false },
      handleScale: { mouseWheel: true, pinch: true, axisPressedMouseMove: { time: true, price: true }, axisDoubleClickReset: true },
    });
    this.candles = this.chart.addSeries(CandlestickSeries, {
      upColor: this.colors.up,
      downColor: this.colors.down,
      borderVisible: false,
      wickUpColor: this.colors.up,
      wickDownColor: this.colors.down,
      priceLineVisible: true,
    });
    if (showVolume) {
      this.volumePane = this.chart.addPane();
      this.volumePane.setStretchFactor(0.22);
      this.volume = this.volumePane.addSeries(HistogramSeries, {
        priceFormat: { type: "volume" },
        lastValueVisible: true,
        priceLineVisible: false,
      });
      // Keep the scale widget present: Lightweight Charts 5.2 dereferences
      // each pane's scale during ResizeObserver layout, and visible:false
      // leaves a null widget that throws "Value is null" on resize.
      this.volume.priceScale().applyOptions({
        borderVisible: false,
        ticksVisible: false,
      });
    }

    this.createVolumeBadge();
    this.emptyData = [];
  }

  createVolumeBadge() {
    this.volBadge = document.createElement("div");
    this.volBadge.className = "chart-vol-badge";
    this.volBadge.setAttribute("aria-hidden", "true");
    this.volBadge.textContent = "Vol: —";
    this.container.parentElement?.appendChild(this.volBadge);

    // Update badge on crosshair hover, reset on mouse leave
    this.chart.subscribeCrosshairMove((param) => {
      if (!param || !param.time) {
        this.updateVolumeBadge();
        return;
      }
      let vol = null;
      if (this.volume && param.seriesData && param.seriesData.has(this.volume)) {
        const entry = param.seriesData.get(this.volume);
        vol = entry?.value;
      } else {
        const item = this.data.find((c) => c.time === param.time);
        vol = item?.volume;
      }
      this.volBadge.textContent = Number.isFinite(vol) ? `Vol: ${formatVolume(vol)}` : "Vol: —";
    });
  }

  updateVolumeBadge(customVol) {
    if (!this.volBadge) return;
    if (Number.isFinite(customVol)) {
      this.volBadge.textContent = `Vol: ${formatVolume(customVol)}`;
      return;
    }
    const latest = this.data.length ? this.data[this.data.length - 1] : null;
    const vol = latest?.volume;
    this.volBadge.textContent = Number.isFinite(vol) ? `Vol: ${formatVolume(vol)}` : "Vol: —";
  }

  toggleSR(forceState) {
    this.showSR = typeof forceState === "boolean" ? forceState : !this.showSR;
    this.refreshSRLines();
    return this.showSR;
  }

  clearSRLines() {
    for (const line of this.srPriceLines) {
      try {
        this.candles.removePriceLine(line);
      } catch {
        // ignore
      }
    }
    this.srPriceLines = [];
  }

  refreshSRLines() {
    this.clearSRLines();
    if (!this.showSR || !this.data.length) return;

    const { supports, resistances } = calculateSRLevels(this.data);
    resistances.forEach((price, idx) => {
      const line = this.candles.createPriceLine({
        price,
        color: this.colors.resistance,
        lineWidth: 1,
        lineStyle: LineStyle.Dashed,
        axisLabelVisible: true,
        title: `R${idx + 1}`,
      });
      this.srPriceLines.push(line);
    });

    supports.forEach((price, idx) => {
      const line = this.candles.createPriceLine({
        price,
        color: this.colors.support,
        lineWidth: 1,
        lineStyle: LineStyle.Dashed,
        axisLabelVisible: true,
        title: `S${idx + 1}`,
      });
      this.srPriceLines.push(line);
    });
  }

  setData(data, { fitSession = true } = {}) {
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
    this.candles.setData(this.data);
    if (this.volume) {
      this.volume.setData(this.data.map((c) => ({
        time: c.time,
        value: Number.isFinite(c.volume) ? c.volume : 0,
        color: c.close >= c.open ? `${this.colors.up}55` : `${this.colors.down}55`,
      })));
    }
    this.updateVolumeBadge();
    this.refreshSRLines();
    if (fitSession && this.data.length) this.fitLatestSession();
  }

  fitLatestSession() {
    if (!this.data.length) return;
    const latest = this.data[this.data.length - 1];
    const latestDay = istDayKey(latest.time);
    const session = this.data.filter((candle) => istDayKey(candle.time) === latestDay);
    // If the latest day has very few bars (e.g. today just started or Kotak 429 delayed today's bars),
    // show at least 25 candles so the user can see recent history rather than a 2-bar stretched void.
    if (!session.length) {
      this.chart.timeScale().fitContent();
      return;
    }
    const last = session[session.length - 1];
    const prior = session.length > 1 ? session[session.length - 2] : null;
    const interval = prior ? Math.max(60, last.time - prior.time) : 300;

    let fromTime = session[0].time - interval;
    if (session.length < 25 && this.data.length > session.length) {
      const windowCandles = this.data.slice(-Math.min(this.data.length, 35));
      fromTime = windowCandles[0].time - interval;
    }

    this.chart.timeScale().setVisibleRange({
      from: fromTime,
      to: last.time + interval * 3,
    });
  }

  update(candle) {
    if (!candle || !Number.isFinite(candle.time)) return;
    this.candles.update({
      time: candle.time, open: candle.open, high: candle.high,
      low: candle.low, close: candle.close,
    });
    if (this.volume) {
      this.volume.update({
        time: candle.time,
        value: Number.isFinite(candle.volume) ? candle.volume : 0,
        color: candle.close >= candle.open ? `${this.colors.up}55` : `${this.colors.down}55`,
      });
    }

    // Keep internal data in sync
    const lastIdx = this.data.length - 1;
    if (lastIdx >= 0 && this.data[lastIdx].time === candle.time) {
      this.data[lastIdx] = { ...this.data[lastIdx], ...candle };
    } else {
      this.data.push(candle);
    }
    this.updateVolumeBadge(candle.volume);
  }

  zoomBy(factor) {
    const scale = this.chart.timeScale();
    const range = scale.getVisibleLogicalRange();
    if (!range) {
      this.fitLatestSession();
      return;
    }
    const center = (range.from + range.to) / 2;
    const halfSpan = Math.max(2, (range.to - range.from) * factor / 2);
    scale.setVisibleLogicalRange({ from: center - halfSpan, to: center + halfSpan });
  }

  zoomIn() { this.zoomBy(0.72); }
  zoomOut() { this.zoomBy(1.38); }
  resetView() { this.fitLatestSession(); }

  applyTheme() {
    this.colors = chartColors(this.container);
    const visibleRange = this.chart.timeScale().getVisibleLogicalRange();
    this.chart.applyOptions({
      layout: { background: { type: "solid", color: this.colors.bg }, textColor: this.colors.text },
      grid: { vertLines: { color: this.colors.grid }, horzLines: { color: this.colors.grid } },
      rightPriceScale: { borderColor: this.colors.border },
      timeScale: { borderColor: this.colors.border },
    });
    this.candles.applyOptions({
      upColor: this.colors.up, wickUpColor: this.colors.up,
      downColor: this.colors.down, wickDownColor: this.colors.down,
    });
    this.setData(this.data, { fitSession: false });
    if (visibleRange) this.chart.timeScale().setVisibleLogicalRange(visibleRange);
  }

  schedule() { this.chart.resize(this.container.clientWidth, this.container.clientHeight); }
  destroy() {
    this.volBadge?.remove();
    this.chart.remove();
  }
}
