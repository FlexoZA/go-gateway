import { Nav } from "@/components/Nav";
import { ConfirmProvider } from "@/components/confirm";
import { DashMain } from "@/components/MappingTest";
import { MappingTestProvider } from "@/contexts/MappingTest";

export default function DashLayout({ children }: { children: React.ReactNode }) {
  return (
    <ConfirmProvider>
      <MappingTestProvider>
        <div className="flex min-h-screen">
          <Nav />
          <DashMain>{children}</DashMain>
        </div>
      </MappingTestProvider>
    </ConfirmProvider>
  );
}
