import type { Metadata } from "next";
import "./globals.css";
import { FontProvider } from "@/components/FontProvider";

export const metadata: Metadata = {
  title: "Markdown Hub",
  description: "Markdown sharing and viewing",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body className="min-h-screen bg-white text-stone-900 antialiased">
        <FontProvider>{children}</FontProvider>
      </body>
    </html>
  );
}
