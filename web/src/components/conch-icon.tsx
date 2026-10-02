import { cn } from "@/lib/utils"

// A conch drawn in one line: a spiral whorl above a tapering canal, with
// the lip's edge beside it — the same drawing as app/icon.svg, in the page
// rather than the tab. It takes its colour from the text around it, so it
// follows the brand in light and dark.
//
// Animated with CSS (see .conch-* in globals.css): the shell rocks now and
// then, and with `waves`, sound rings out of the opening like a conch being
// blown. Motion stops when the viewer prefers reduced motion.
//
// The coordinates are the icon's own 512 space, cropped to the drawing so
// it fills the box it is given instead of sitting in the margin the square
// artboard leaves around it.
export function ConchIcon({
  className,
  animated = true,
  waves = false,
}: {
  className?: string
  animated?: boolean
  waves?: boolean
}) {
  return (
    <svg
      viewBox={waves ? "160 80 400 350" : "160 80 210 350"}
      aria-hidden
      className={cn("overflow-visible", animated && "conch-animated", className)}
      fill="none"
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      {waves && (
        <g strokeWidth="13" opacity="0.85">
          <path className="conch-wave" d="M392 236 C420 264 420 304 392 332" />
          <path className="conch-wave conch-wave-2" d="M432 206 C476 254 476 314 432 362" />
          <path className="conch-wave conch-wave-3" d="M472 176 C532 244 532 324 472 392" />
        </g>
      )}

      <g className="conch-shell" strokeWidth="18">
        <path d="M 263.73 183.93 L 265.87 186.03 L 267.31 189.12 L 267.70 192.93 L 266.77 197.10 L 264.38 201.17 L 260.54 204.65 L 255.44 207.06 L 249.39 207.96 L 242.87 207.04 L 236.43 204.12 L 230.71 199.21 L 226.30 192.50 L 223.77 184.39 L 223.55 175.40 L 225.92 166.23 L 230.93 157.62 L 238.43 150.36 L 248.05 145.17 L 259.19 142.65 L 271.08 143.25 L 282.84 147.17 L 293.52 154.38 L 302.20 164.55 L 308.05 177.12 L 310.41 191.28 L 308.86 206.04 L 303.25 220.32 L 293.75 232.98 L 280.84 242.95 L 265.29 249.32 L 248.11 251.39 L 230.51 248.76 L 213.80 241.35 L 199.26 229.46 L 188.12 213.74 L 181.36 195.17 L 179.72 174.99 L 183.58 154.60 L 192.91 135.52 L 207.28 119.22 L 225.86 107.02 L 247.47 100.02 L 270.65 98.96 L 293.77 104.16 L 315.14 115.52 L 333.10 132.46 L 346.21 153.96 L 353.32 178.63 L 353.68 204.79 L 346.99 230.60 L 333.49 254.16 L 313.90 273.67 L 302.18 281.41 C 328.18 327.41 304.00 358.00 258 414" />
        <path d="M 208 286 L 242 386" />
      </g>
    </svg>
  )
}
