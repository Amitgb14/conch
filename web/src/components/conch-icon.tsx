import { useId } from "react"

// A conch shell: stacked whorls, a round body whorl and an opening, with
// the sutures and opening cut out so it sits on any background.
export function ConchIcon({ className, ...props }: React.ComponentProps<"svg">) {
  const mask = useId()
  return (
    <svg viewBox="0 0 64 64" aria-hidden className={className} {...props}>
      <defs>
        <mask id={mask}>
          <rect width="64" height="64" fill="white" />
          <g transform="rotate(-32 32 32)" stroke="black" strokeLinecap="round" fill="none">
            <path d="M28.4 9 C30.6 10.4 33.4 10.4 35.6 9" strokeWidth="1.6" />
            <path d="M23.2 15.6 C27.4 18 36.6 18 40.8 15.6" strokeWidth="1.8" />
            <path d="M16 24.8 C23 29.4 41 29.4 48 24.8" strokeWidth="2" />
            <path d="M34 34 C40 32 50 34 51 42 C52 50 45 56 38 56.5 C35 50 33 42 34 34 Z" fill="black" stroke="none" />
          </g>
        </mask>
      </defs>
      <g transform="rotate(-32 32 32)" mask={`url(#${mask})`}>
        <path
          fill="currentColor"
          d="M32 1.5 C33.8 4 35 6.5 35.6 9 C38.2 10.2 40 12.6 40.8 15.6 C44.6 17.2 47.2 20.6 48 24.8 C53.4 28 56.5 33.5 56.5 40 C56.5 50 48.5 57.5 38 60.5 L32 63 L26 60.5 C15.5 57.5 7.5 50 7.5 40 C7.5 33.5 10.6 28 16 24.8 C16.8 20.6 19.4 17.2 23.2 15.6 C24 12.6 25.8 10.2 28.4 9 C29 6.5 30.2 4 32 1.5 Z"
        />
      </g>
      <g transform="rotate(-32 32 32)">
        <path
          fill="currentColor"
          opacity=".35"
          d="M37 38 C42 37.5 47 40 47 45 C47 49.5 43 52.5 39.5 52.8 C38 48 37 43 37 38 Z"
        />
      </g>
    </svg>
  )
}
