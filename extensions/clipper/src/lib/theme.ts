/** Mirror Miru tokens so the floating card matches the reader. */
export type MiruTheme = 'light' | 'dark';

export type MiruTokens = {
  paper: string;
  paperRaised: string;
  ink: string;
  inkSoft: string;
  muted: string;
  accent: string;
  accentSoft: string;
  line: string;
  inputBg: string;
  shadow: string;
  hl: string;
};

export function detectMiruTheme(): MiruTheme {
  const attr = document.documentElement.getAttribute('data-theme');
  if (attr === 'dark' || attr === 'light') return attr;
  if (window.matchMedia?.('(prefers-color-scheme: dark)').matches) return 'dark';
  return 'light';
}

export const MIRU_TOKENS: Record<MiruTheme, MiruTokens> = {
  light: {
    paper: '#f5f4ed',
    paperRaised: '#faf9f5',
    ink: '#171714',
    inkSoft: '#55534d',
    muted: '#77746d',
    accent: '#1b365d',
    accentSoft: '#e8edf3',
    line: '#dedbd0',
    inputBg: '#ffffff',
    shadow: 'rgba(0, 0, 0, 0.08)',
    hl: 'rgba(255, 214, 102, 0.42)',
  },
  dark: {
    paper: '#141413',
    paperRaised: '#1e1e1c',
    ink: '#e8e6dc',
    inkSoft: '#b8b6ac',
    muted: '#8b897f',
    accent: '#84a4c4',
    accentSoft: '#283345',
    line: '#2f2e29',
    inputBg: '#181816',
    shadow: 'rgba(0, 0, 0, 0.35)',
    hl: 'rgba(255, 214, 102, 0.20)',
  },
};
