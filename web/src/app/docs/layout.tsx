import { DocsBreadcrumb, DocsPager } from "@/components/docs-pager"
import { DocsSidebar } from "@/components/docs-sidebar"
import { DocsToc } from "@/components/docs-toc"

export default function DocsLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="mx-auto grid w-full max-w-7xl gap-10 px-5 sm:px-6 md:grid-cols-[200px_minmax(0,1fr)] xl:grid-cols-[220px_minmax(0,1fr)_200px]">
      <aside className="sticky top-14 hidden h-[calc(100vh-3.5rem)] overflow-y-auto py-10 md:block">
        <DocsSidebar />
      </aside>
      <article data-docs className="min-w-0 py-10 md:py-12">
        <div className="mx-auto max-w-3xl">
          <DocsBreadcrumb />
          {children}
          <DocsPager />
        </div>
      </article>
      <aside className="sticky top-14 hidden h-[calc(100vh-3.5rem)] overflow-y-auto py-12 xl:block">
        <DocsToc />
      </aside>
    </div>
  )
}
