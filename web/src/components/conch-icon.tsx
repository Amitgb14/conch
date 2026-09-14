import { useId } from "react"

import { cn } from "@/lib/utils"

// A queen conch: a knobbed spire pointing left, a tan body whorl and a
// flared pink lip. Animated with CSS (see .conch-* in globals.css): the
// shell rocks now and then and its mouth glows; with `waves`, sound rings
// out of the opening like a conch being blown. Motion stops when the
// viewer prefers reduced motion.
export function ConchIcon({
  className,
  animated = true,
  waves = false,
}: {
  className?: string
  animated?: boolean
  waves?: boolean
}) {
  const id = useId().replace(/:/g, "")
  const url = (name: string) => `url(#${id}-${name})`
  return (
    <svg
      viewBox={waves ? "0 0 128 96" : "0 0 96 96"}
      aria-hidden
      className={cn("overflow-visible", animated && "conch-animated", className)}
    >
      <defs>
        <linearGradient id={`${id}-lip`} x1="0.15" y1="0" x2="0.85" y2="1">
          <stop offset="0" stopColor="#ffe1cc" />
          <stop offset="0.5" stopColor="#f9a594" />
          <stop offset="1" stopColor="#e96a7f" />
        </linearGradient>
        <radialGradient id={`${id}-mouth`} cx="0.66" cy="0.5" r="0.5">
          <stop offset="0" stopColor="#d9446a" stopOpacity="0.85" />
          <stop offset="1" stopColor="#f58f93" stopOpacity="0" />
        </radialGradient>
        <linearGradient id={`${id}-body`} x1="0.1" y1="0.1" x2="0.9" y2="0.9">
          <stop offset="0" stopColor="#f9e0b4" />
          <stop offset="0.55" stopColor="#eab274" />
          <stop offset="1" stopColor="#cf8744" />
        </linearGradient>
        <linearGradient id={`${id}-spire`} x1="0" y1="0" x2="1" y2="0.3">
          <stop offset="0" stopColor="#fff7ea" />
          <stop offset="1" stopColor="#f1d3a6" />
        </linearGradient>
      </defs>

      {waves && (
        <g fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round">
          <path className="conch-wave" d="M96 22 C104 30 106 44 102 56" />
          <path className="conch-wave conch-wave-2" d="M104 12 C116 24 119 44 112 62" />
          <path className="conch-wave conch-wave-3" d="M112 3 C127 19 131 45 122 68" />
        </g>
      )}

      <g className="conch-shell" strokeLinejoin="round">
        <path
          d="M24 32 C27 17 37 6 47 6 C63 7 81 23 88 44 C92 55 92 65 90 72 L86 67 C74 52 58 38 43 28 Z"
          fill={url("lip")}
          stroke="#c9536b"
          strokeWidth="1.2"
        />
        <path
          className="conch-glow"
          d="M42 26 C52 20 66 26 76 40 C82 49 85 58 86 64 C75 51 60 38 46 30 Z"
          fill={url("mouth")}
        />
        <path
          d="M25 33 C29 28 33 25 35 24 L37 16 L41 26 C45 28 49 30 52 32 C66 42 79 54 88 65 C92 70 93 75 89 79 C86 81 81 81 78 77 C69 74 57 73 45 76 C41 78 38 83 36 88 C34 90 31 89 31 85 C31 80 30 74 27 70 C24 66 22 62 21 57 Z"
          fill={url("body")}
          stroke="#a8652f"
          strokeWidth="1.2"
        />
        <path
          d="M41 34 C54 42 68 54 80 68 M35 45 C48 51 61 61 71 72 M31 58 C41 62 51 67 59 73"
          fill="none"
          stroke="#b87538"
          strokeWidth="1"
          strokeLinecap="round"
          opacity="0.5"
        />
        <path
          d="M29 30 L27 26.5 L24.5 31 L21 31 L19.5 28 L17 32.5 L13.5 33.5 L12 31 L10.5 35 C8.5 35.7 6.5 36.5 4.5 37.5 C6 39.5 8 41 10 42 L10.5 45.5 L13 43.5 L16 46 L16.5 49.5 L19.5 48.5 L21.5 52.5 L24 50.5 L27 54 C26.5 46 27 38 29 30 Z"
          fill={url("spire")}
          stroke="#b58250"
          strokeWidth="1.1"
        />
        <path
          d="M9 38.5 C14 39.5 20 40.5 27 41"
          fill="none"
          stroke="#c99a66"
          strokeWidth="0.9"
          strokeLinecap="round"
          opacity="0.7"
        />
        <path
          d="M25 59 L18.5 62.5 L25.5 64.5 Z M27.5 68 L22 73 L29 72.5 Z"
          fill="#efc88f"
          stroke="#a8652f"
          strokeWidth="1"
        />
      </g>
    </svg>
  )
}
