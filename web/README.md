# conch website

The landing page and documentation for conch: Next.js (App Router, static
export), Tailwind CSS and shadcn/ui on Base UI.

```sh
pnpm install
pnpm dev          # http://localhost:3000
pnpm build        # static site in out/
```

Set `BASE_PATH=/conch` when building for a subpath such as GitHub Pages.

## Layout

```
src/app/page.tsx            landing page
src/app/docs/**/page.mdx    documentation, one MDX file per page
src/lib/docs.ts             docs sidebar and page order
src/mdx-components.tsx      how markdown elements render; Callout, Steps, Step
src/components/landing/     landing page sections (TUI demo, features, brain demo)
src/components/ui/          shadcn/ui components (pnpm dlx shadcn add …)
```

To add a docs page, create `src/app/docs/<slug>/page.mdx` starting with
`export const metadata = { title, description }` and add it to
`src/lib/docs.ts`.
