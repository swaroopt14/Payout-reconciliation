import type { Config } from 'tailwindcss'

const config: Config = {
  content: [
    './pages/**/*.{js,ts,jsx,tsx,mdx}',
    './components/**/*.{js,ts,jsx,tsx,mdx}',
    './app/**/*.{js,ts,jsx,tsx,mdx}',
    './src/**/*.{js,ts,jsx,tsx,mdx}',
    './services/**/*.{js,ts,jsx,tsx,mdx}',
  ],
  theme: {
    extend: {
      colors: {
        'zord-blue': {
          50: '#EFF6FF',
          500: '#3B82F6',
          600: '#2563EB',
          700: '#1D4ED8',
          800: '#1E3A8A',
        },
        'zord-base': {
          main: '#0B1220',
          panel: '#111827',
          table: '#0F172A',
          border: '#1F2937',
          text: {
            primary: '#E5E7EB',
            secondary: '#9CA3AF',
          },
        },
        'zord-status': {
          healthy: '#16A34A',
          degraded: '#CA8A04',
          failed: '#DC2626',
          active: '#2563EB',
          neutral: '#9CA3AF',
        },
        'zord-destructive': {
          bg: '#7F1D1D',
          text: '#FECACA',
        },
      },
      borderRadius: {
        'zord': '4px',
      },
      keyframes: {
        'indeterminate': {
          '0%': { transform: 'translateX(-100%)' },
          '100%': { transform: 'translateX(250%)' },
        },
      },
      animation: {
        'indeterminate': 'indeterminate 1.8s ease-in-out infinite',
      },
    },
  },
  plugins: [],
}
export default config
