import { computed, ref, watch } from 'vue'

const storageKey = 'dochub-color-mode'
type ColorMode = 'light' | 'dark'

function preferredMode(): ColorMode {
  if (typeof window === 'undefined') return 'light'
  const saved = window.localStorage.getItem(storageKey)
  if (saved === 'light' || saved === 'dark') return saved
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

const colorMode = ref<ColorMode>(preferredMode())
const isDark = computed(() => colorMode.value === 'dark')

function applyMode(mode: ColorMode) {
  if (typeof document === 'undefined') return
  document.documentElement.dataset.theme = mode
  document.documentElement.style.colorScheme = mode
  const themeColor = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
  themeColor?.setAttribute('content', mode === 'dark' ? '#1b1b1f' : '#ffffff')
}

watch(
  colorMode,
  (mode) => {
    applyMode(mode)
    if (typeof window !== 'undefined') window.localStorage.setItem(storageKey, mode)
  },
  { immediate: true },
)

export function useTheme() {
  function toggle() {
    colorMode.value = colorMode.value === 'dark' ? 'light' : 'dark'
  }

  return { colorMode, isDark, toggle }
}
