import { Link } from "@tanstack/react-router"

// Any path the route tree doesn't know
export function NotFound() {
  return (
    <div className="min-h-screen bg-background pt-13 lg:pt-19">
      <main className="flex flex-col gap-4 p-5 pt-8 text-text-primary lg:p-page">
        <h1 className="text-2xl md:text-3xl">Page not found</h1>
        <Link to="/projects" className="self-start text-sm underline underline-offset-2 hover:text-highlight">
          Back to Projects
        </Link>
      </main>
    </div>
  )
}
