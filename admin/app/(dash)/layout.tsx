import { Nav } from "@/components/Nav";
import { ConfirmProvider } from "@/components/confirm";

export default function DashLayout({ children }: { children: React.ReactNode }) {
  return (
    <ConfirmProvider>
      <div className="flex min-h-screen">
        <Nav />
        <main className="flex-1 overflow-auto p-6 md:p-8">{children}</main>
      </div>
    </ConfirmProvider>
  );
}
