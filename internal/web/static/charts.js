import { CandlestickSeries, HistogramSeries, createChart } from "./lightweight-charts.standalone.production.js";

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
  };
};

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
  constructor(container, { showVolume = true } = {}) {
    this.container = container;
    this.data = [];
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
        lastValueVisible: false,
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
    this.emptyData = [];
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
    if (fitSession && this.data.length) this.fitLatestSession();
  }

  fitLatestSession() {
    if (!this.data.length) return;
    const latest = this.data[this.data.length - 1];
    const latestDay = istDayKey(latest.time);
    const session = this.data.filter((candle) => istDayKey(candle.time) === latestDay);
    if (!session.length) {
      this.chart.timeScale().fitContent();
      return;
    }
    const last = session[session.length - 1];
    const prior = session.length > 1 ? session[session.length - 2] : null;
    const interval = prior ? Math.max(60, last.time - prior.time) : 300;
    this.chart.timeScale().setVisibleRange({
      from: session[0].time - interval,
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
  destroy() { this.chart.remove(); }
}
