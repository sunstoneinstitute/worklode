import { useEffect, useState } from "react"

const STORAGE_KEY = "worklode.theme"

function storedDark(): boolean {
  try {
    return localStorage.getItem(STORAGE_KEY) === "dark"
  } catch {
    return false
  }
}

/** The light/dark choice, kept in localStorage under worklode.theme (WL-REQ-1772). */
export function useDarkMode() {
  const [isDark, setIsDark] = useState(storedDark)

  useEffect(() => {
    document.documentElement.classList.toggle("dark", isDark)
    try {
      localStorage.setItem(STORAGE_KEY, isDark ? "dark" : "light")
    } catch {
      // Storage blocked: the choice lasts until reload
    }
  }, [isDark])

  return { isDark, toggle: () => setIsDark((prev) => !prev) }
}
