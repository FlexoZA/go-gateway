"use client";

import { PageHeader } from "@/components/ui";
import { MappingControls, MappingEvents } from "@/components/MappingTest";

// The Mapping Test page is a thin view over the shared MappingTest provider (see
// contexts/MappingTest.tsx). The test lifecycle lives in that provider so it
// survives navigation: start a test here, jump to Device Mapping, and keep
// watching the live feed in the right-side drawer.

export default function MappingTestPage() {
  return (
    <div>
      <PageHeader
        title="Mapping Test"
        subtitle="Trigger events on a live device and watch each raw code resolve through your mappings in real time. Unmapped codes are flagged so you can add them. Leave this page while running and the feed follows you in a drawer."
      />
      <MappingControls />
      <MappingEvents />
    </div>
  );
}
