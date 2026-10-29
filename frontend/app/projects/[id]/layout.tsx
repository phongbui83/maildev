// Static export (lite edition) needs at least one concrete value for the
// dynamic segment; the pages are client components that read the real slug
// from the URL at runtime, so one pre-rendered shell serves every project
// (the lite server maps /projects/<anything>/... onto these files).
export function generateStaticParams() {
  return [{ id: "default" }];
}

export default function ProjectLayout({ children }: { children: React.ReactNode }) {
  return children;
}
