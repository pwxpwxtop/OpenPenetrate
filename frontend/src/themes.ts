export type ThemeMode = 'dark' | 'light'

export interface ThemePreset {
  id: string
  name: string
  mode: ThemeMode
  background: string
  accent: string
  accent2: string
  accentSoft: string
  accentBorder: string
  text: string
  muted: string
  border: string
  panel: string
  panelStrong: string
  sidebarBg: string
}

interface Palette {
  name: string
  hue: number
}

const palettes: Palette[] = [
  { name: '深海蓝', hue: 215 },
  { name: '晴空', hue: 205 },
  { name: '天青', hue: 195 },
  { name: '湖蓝', hue: 185 },
  { name: '青碧', hue: 170 },
  { name: '翡翠', hue: 155 },
  { name: '森绿', hue: 140 },
  { name: '翠竹', hue: 120 },
  { name: '嫩绿', hue: 100 },
  { name: '青柠', hue: 80 },
  { name: '麦黄', hue: 55 },
  { name: '金沙', hue: 45 },
  { name: '琥珀', hue: 35 },
  { name: '橙霞', hue: 22 },
  { name: '朱砂', hue: 8 },
  { name: '熔岩', hue: 15 },
  { name: '绯红', hue: 350 },
  { name: '樱粉', hue: 335 },
  { name: '蔷薇', hue: 325 },
  { name: '玫瑰', hue: 315 },
  { name: '品红', hue: 300 },
  { name: '霓虹紫', hue: 285 },
  { name: '兰紫', hue: 275 },
  { name: '罗马紫', hue: 265 },
  { name: '紫罗兰', hue: 255 },
  { name: '星夜', hue: 245 },
  { name: '靛蓝', hue: 235 },
  { name: '宝蓝', hue: 225 },
  { name: '海蓝', hue: 220 },
  { name: '石墨', hue: 210 },
]

function hsla(hue: number, saturation: number, lightness: number, alpha: number) {
  return `hsla(${hue}, ${saturation}%, ${lightness}%, ${alpha})`
}

const darkThemes: ThemePreset[] = palettes.map((palette, index) => {
  const secondaryHue = (palette.hue + 35) % 360
  return {
    id: `dark-${index}`,
    name: `暗色 · ${palette.name}`,
    mode: 'dark',
    background: `radial-gradient(900px 520px at 88% -12%, ${hsla(palette.hue, 85, 62, 0.16)}, transparent 62%), radial-gradient(760px 460px at -8% 108%, ${hsla(secondaryHue, 80, 66, 0.12)}, transparent 60%), #0b0e17`,
    accent: `hsl(${palette.hue} 85% 62%)`,
    accent2: `hsl(${secondaryHue} 80% 66%)`,
    accentSoft: hsla(palette.hue, 85, 62, 0.18),
    accentBorder: hsla(palette.hue, 85, 62, 0.36),
    text: '#e9edf7',
    muted: '#8d97ad',
    border: 'rgba(255, 255, 255, 0.09)',
    panel: 'rgba(255, 255, 255, 0.035)',
    panelStrong: 'rgba(255, 255, 255, 0.06)',
    sidebarBg: 'rgba(8, 11, 19, 0.58)',
  }
})

const lightThemes: ThemePreset[] = palettes.map((palette, index) => {
  const secondaryHue = (palette.hue + 35) % 360
  return {
    id: `light-${index}`,
    name: `浅色 · ${palette.name}`,
    mode: 'light',
    background: `radial-gradient(900px 520px at 88% -12%, ${hsla(palette.hue, 80, 62, 0.2)}, transparent 62%), radial-gradient(760px 460px at -8% 108%, ${hsla(secondaryHue, 75, 62, 0.16)}, transparent 60%), #f4f7fc`,
    accent: `hsl(${palette.hue} 75% 45%)`,
    accent2: `hsl(${secondaryHue} 70% 50%)`,
    accentSoft: hsla(palette.hue, 75, 45, 0.14),
    accentBorder: hsla(palette.hue, 75, 45, 0.3),
    text: '#182030',
    muted: '#5f6b80',
    border: 'rgba(20, 32, 52, 0.1)',
    panel: 'rgba(255, 255, 255, 0.72)',
    panelStrong: 'rgba(255, 255, 255, 0.92)',
    sidebarBg: 'rgba(255, 255, 255, 0.62)',
  }
})

export const themePresets: ThemePreset[] = [...darkThemes, ...lightThemes]
