// Ambient type declarations for Scalper browser runtime

declare global {
  interface Window {
    S?: any;
    CACHE?: any;
    klinecharts?: any;
    currentChart?: any;
    optionChart?: any;
    appState?: any;
    webkitAudioContext?: typeof AudioContext;
    handleAppBackButton?: any;
    Capacitor?: any;
  }

  interface Navigator {
    standalone?: boolean;
    userAgentData?: any;
  }

  const klinecharts: any;
  const S: any;
  const CACHE: any;

  interface Element {
    value?: any;
    disabled?: any;
    content?: any;
    checked?: any;
    title?: any;
    dataset?: any;
    selectionStart?: any;
    selectionEnd?: any;
    setRangeText?: any;
    closest(selector: string): any;
  }

  interface HTMLElement {
    value?: any;
    disabled?: any;
    content?: any;
    checked?: any;
    selectionStart?: any;
    selectionEnd?: any;
    setRangeText?: any;
  }
}

export {};
