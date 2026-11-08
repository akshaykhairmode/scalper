// Web Audio API Synthesizer for trading sounds

let audioCtx: AudioContext | null = null;

export function getAudioContext(): AudioContext | null {
  if (!audioCtx) {
    const AudioContextClass = window.AudioContext || (window as any).webkitAudioContext;
    if (AudioContextClass) {
      audioCtx = new AudioContextClass();
    }
  }
  if (audioCtx && audioCtx.state === "suspended") {
    audioCtx.resume().catch(() => {});
  }
  return audioCtx;
}

export function initAudioOnFirstGesture(): void {
  const unlock = () => {
    const ctx = getAudioContext();
    if (ctx && ctx.state === "suspended") {
      ctx.resume().catch(() => {});
    }
    window.removeEventListener("pointerdown", unlock);
    window.removeEventListener("keydown", unlock);
  };
  window.addEventListener("pointerdown", unlock, { once: true });
  window.addEventListener("keydown", unlock, { once: true });
}

export function playTone(
  ctx: AudioContext,
  freq: number,
  startTime: number,
  duration: number,
  maxGain = 0.15,
  type: OscillatorType = "sine"
): void {
  try {
    const osc = ctx.createOscillator();
    const gain = ctx.createGain();

    osc.type = type;
    osc.frequency.setValueAtTime(freq, startTime);

    gain.gain.setValueAtTime(0.0001, startTime);
    gain.gain.exponentialRampToValueAtTime(maxGain, startTime + 0.005);
    gain.gain.exponentialRampToValueAtTime(0.0001, startTime + duration);

    osc.connect(gain);
    gain.connect(ctx.destination);

    osc.start(startTime);
    osc.stop(startTime + duration + 0.02);
  } catch (_) {}
}

export function playTradeSound(side: string, enabled = true): void {
  if (!enabled) return;
  const ctx = getAudioContext();
  if (!ctx) return;
  if (ctx.state === "suspended") {
    ctx.resume().catch(() => {});
  }

  const now = ctx.currentTime;
  const action = String(side || "").toLowerCase();

  if (action === "buy" || action === "b") {
    // Upbeat ascending chime: D5 (587.33 Hz) -> A5 (880.00 Hz)
    playTone(ctx, 587.33, now, 0.08, 0.16, "sine");
    playTone(ctx, 880.0, now + 0.07, 0.18, 0.18, "sine");
  } else if (action === "sell" || action === "s") {
    // Crisp descending chime: A5 (880.00 Hz) -> C5 (523.25 Hz)
    playTone(ctx, 880.0, now, 0.08, 0.16, "sine");
    playTone(ctx, 523.25, now + 0.07, 0.18, 0.18, "sine");
  } else if (action === "target" || action === "tp") {
    // Celebratory high chime: E5 (659.25 Hz) -> B5 (987.77 Hz) -> E6 (1318.5 Hz)
    playTone(ctx, 659.25, now, 0.07, 0.14, "sine");
    playTone(ctx, 987.77, now + 0.06, 0.09, 0.16, "sine");
    playTone(ctx, 1318.5, now + 0.14, 0.16, 0.20, "sine");
  } else if (action === "reject" || action === "error") {
    // Low warning pulse: 220 Hz -> 175 Hz
    playTone(ctx, 220.0, now, 0.08, 0.14, "triangle");
    playTone(ctx, 175.0, now + 0.09, 0.12, 0.14, "triangle");
  }
}
