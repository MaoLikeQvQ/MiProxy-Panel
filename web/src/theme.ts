import { ref, watchEffect } from 'vue'

const key = 'miproxy-panel-appearance'

export function useAppearance() {
  const dark = ref(false)
  try {
    dark.value = localStorage.getItem(key)?.startsWith('dark') ?? false
  } catch {
    // The default theme is available with storage disabled.
  }

  watchEffect(() => {
    const theme = dark.value ? 'dark' : 'default'
    document.documentElement.dataset.theme = theme
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', dark.value ? '#10151e' : '#ffffff')
    try {
      localStorage.setItem(key, theme)
    } catch {
      // The selection still applies to the current page.
    }
  })
  return dark
}
