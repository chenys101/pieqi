import type { Config } from 'tailwindcss'

// Tailwind 设计令牌：颜色一律走 token（SPEC §3 / 方案 §29/§30），
// 业务组件禁止写任意色值（bg-[#...]）。
//
// 色名与 SPEC §3.1/§3.2 的层级一一对应，别再退回「一个 muted 打天下」：
//   表面  background(canvas) / surface / surface-subtle / surface-muted / surface-inset
//   边框  border-subtle / border / border-strong
//   文字  text / text-secondary / text-tertiary / text-disabled
// `elevated` 与 `muted` 是旧类名的兼容别名（分别指向 surface-muted / text-secondary），
// 新代码请直接用层级名。
export default {
  content: ['./index.html', './src/**/*.{vue,ts}'],
  // 主题靠 <html data-theme="light|dark"> 切换；不用 .dark class。
  darkMode: ['class', '[data-theme="dark"]'],
  theme: {
    extend: {
      colors: {
        // 表面
        background: 'rgb(var(--color-background) / <alpha-value>)',
        surface: 'rgb(var(--color-surface) / <alpha-value>)',
        'surface-subtle': 'rgb(var(--color-surface-subtle) / <alpha-value>)',
        'surface-muted': 'rgb(var(--color-surface-muted) / <alpha-value>)',
        'surface-inset': 'rgb(var(--color-surface-inset) / <alpha-value>)',
        elevated: 'rgb(var(--color-elevated) / <alpha-value>)',
        // 边框
        border: 'rgb(var(--color-border) / <alpha-value>)',
        'border-subtle': 'rgb(var(--color-border-subtle) / <alpha-value>)',
        'border-strong': 'rgb(var(--color-border-strong) / <alpha-value>)',
        // 文字
        text: 'rgb(var(--color-text) / <alpha-value>)',
        'text-secondary': 'rgb(var(--color-text-secondary) / <alpha-value>)',
        'text-tertiary': 'rgb(var(--color-text-tertiary) / <alpha-value>)',
        'text-disabled': 'rgb(var(--color-text-disabled) / <alpha-value>)',
        muted: 'rgb(var(--color-text-muted) / <alpha-value>)',
        // 语义
        success: 'rgb(var(--color-success) / <alpha-value>)',
        warning: 'rgb(var(--color-warning) / <alpha-value>)',
        error: 'rgb(var(--color-error) / <alpha-value>)',
        info: 'rgb(var(--color-info) / <alpha-value>)',
        // 强调
        accent: 'rgb(var(--color-accent) / <alpha-value>)',
        'accent-hover': 'rgb(var(--color-accent-hover) / <alpha-value>)',
        'accent-active': 'rgb(var(--color-accent-active) / <alpha-value>)',
      },
      // 阴影走 token：深色下是 none（层次交给边框），浅色下才有值（SPEC §3.5）
      boxShadow: {
        xs: 'var(--shadow-xs)',
        sm: 'var(--shadow-sm)',
        md: 'var(--shadow-md)',
        lg: 'var(--shadow-lg)',
      },
      fontFamily: {
        sans: [
          '-apple-system',
          'BlinkMacSystemFont',
          'Segoe UI',
          'PingFang SC',
          'Hiragino Sans GB',
          'Microsoft YaHei',
          'sans-serif',
        ],
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'Consolas', 'monospace'],
      },
    },
  },
  plugins: [],
} satisfies Config
