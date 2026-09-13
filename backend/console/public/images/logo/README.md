# App mark assets

This folder keeps the original tilted mark geometry from the uploaded Canva image, converted into clean vector assets for the console.

## Files

- `zord-mark-exact-currentColor.svg` — best for React/Next.js; color controlled by CSS `color`
- `zord-mark-exact-black.svg` — black mark for light backgrounds
- `zord-mark-exact-white.svg` — white mark for dark backgrounds
- `zord-mark-exact-tight-black.svg` — tight viewBox for compact UI
- `zord-app-icon-dark.svg` — square safe-area app icon
- `zord-favicon-dark.svg` — favicon-safe square mark
- `ZordMark.tsx` — React/TypeScript component

Filenames are unchanged so existing imports keep working.

```css
.app-mark {
  color: currentColor;
}
```
