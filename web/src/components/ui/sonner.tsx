import { useEffect, useState } from "react"
import { Toaster as Sonner, type ToasterProps } from "sonner"

// The shadcn original reads the theme from next-themes; this app has neither
// Next nor a theme context, so the toaster watches the same `.dark` class on the
// document that everything else keys off — following both the system default and
// a manual toggle.
function useDocumentTheme(): "light" | "dark" {
  const [theme, setTheme] = useState<"light" | "dark">(() =>
    document.documentElement.classList.contains("dark") ? "dark" : "light"
  )

  useEffect(() => {
    const root = document.documentElement
    const observer = new MutationObserver(() =>
      setTheme(root.classList.contains("dark") ? "dark" : "light")
    )
    observer.observe(root, { attributes: true, attributeFilter: ["class"] })
    return () => observer.disconnect()
  }, [])

  return theme
}

function Toaster({ ...props }: ToasterProps) {
  const theme = useDocumentTheme()

  return (
    <Sonner
      theme={theme}
      className="toaster group"
      position="bottom-center"
      richColors
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-text": "var(--popover-foreground)",
          "--normal-border": "var(--border)",
        } as React.CSSProperties
      }
      {...props}
    />
  )
}

export { Toaster }
