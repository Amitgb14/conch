import type { MDXComponents } from "mdx/types"
import Link from "next/link"

import { Callout } from "@/components/callout"
import { CodeBlock } from "@/components/code-block"
import { Step, Steps } from "@/components/steps"
import { cn } from "@/lib/utils"

const components: MDXComponents = {
  h1: ({ className, ...props }) => (
    <h1 className={cn("mb-3 text-3xl font-semibold tracking-[-0.025em] sm:text-4xl", className)} {...props} />
  ),
  h2: ({ className, ...props }) => (
    <h2
      className={cn("mt-12 mb-3 scroll-mt-20 border-b pb-2 text-2xl font-semibold tracking-tight", className)}
      {...props}
    />
  ),
  h3: ({ className, ...props }) => (
    <h3 className={cn("mt-8 mb-2 scroll-mt-20 text-lg font-semibold tracking-tight", className)} {...props} />
  ),
  p: ({ className, ...props }) => <p className={cn("my-4 leading-7", className)} {...props} />,
  a: ({ href = "", className, ...props }) => {
    const cls = cn("font-medium underline underline-offset-4 decoration-border hover:decoration-foreground", className)
    return href.startsWith("/") || href.startsWith("#") ? (
      <Link href={href} className={cls} {...props} />
    ) : (
      <a href={href} className={cls} {...props} />
    )
  },
  ul: ({ className, ...props }) => <ul className={cn("my-4 ml-5 list-disc [&>li]:mt-2", className)} {...props} />,
  ol: ({ className, ...props }) => <ol className={cn("my-4 ml-5 list-decimal [&>li]:mt-2", className)} {...props} />,
  li: ({ className, ...props }) => <li className={cn("leading-7 marker:text-muted-foreground", className)} {...props} />,
  blockquote: ({ className, ...props }) => (
    <blockquote className={cn("my-5 border-l-2 pl-4 text-muted-foreground italic", className)} {...props} />
  ),
  hr: () => <hr className="my-10" />,
  code: ({ className, ...props }) => (
    <code
      className={cn("rounded-md border bg-muted px-[0.35em] py-[0.1em] font-mono text-[0.85em]", className)}
      {...props}
    />
  ),
  pre: CodeBlock,
  table: ({ className, ...props }) => (
    <div className="my-6 w-full overflow-x-auto rounded-xl border">
      <table className={cn("w-full border-collapse text-sm", className)} {...props} />
    </div>
  ),
  thead: ({ className, ...props }) => <thead className={cn("bg-muted/60", className)} {...props} />,
  tr: ({ className, ...props }) => <tr className={cn("border-b last:border-0", className)} {...props} />,
  th: ({ className, ...props }) => (
    <th className={cn("px-3 py-2 text-left align-top font-medium whitespace-nowrap", className)} {...props} />
  ),
  td: ({ className, ...props }) => <td className={cn("px-3 py-2 align-top leading-6", className)} {...props} />,
  Callout,
  Steps,
  Step,
}

export function useMDXComponents(): MDXComponents {
  return components
}
