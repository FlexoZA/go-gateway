import { Nav } from "@/components/Nav";

export default function DashLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-screen">
      <Nav />
      <main className="flex-1 overflow-auto p-6 md:p-8">{children}</main>
    </div>
  );
}
