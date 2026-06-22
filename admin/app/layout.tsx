import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Device Gateway Admin",
  description: "Administration panel for the device gateway",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
