import { PageHeader } from "@/components/ui";
import { ConsoleShell } from "@/components/console/console-shell";

export const metadata = { title: "API Console" };

export default function ApiConsolePage() {
  return (
    <div>
      <PageHeader
        title="API Console"
        subtitle="Run any request against the gateway API. Requests are authorized with the service key server-side."
      />
      <ConsoleShell />
    </div>
  );
}
