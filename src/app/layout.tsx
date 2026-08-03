import type { Metadata } from "next";
import "./globals.css";
import { FontProvider } from "@/components/FontProvider";
import { THEME_MODES, THEME_PRESETS } from "@/lib/themes";

export const metadata: Metadata = {
  title: "Markdown Hub",
  description: "Markdown sharing and viewing",
  icons: {
    icon: "/mdhub/mdhub-logo.svg",
    shortcut: "/mdhub/mdhub-logo.svg",
  },
};

// Apply stored reader prefs (theme / mode / font / size / width) before first paint
// to avoid a flash of the default light, narrow, system-font rendering.
const VALID_THEME_MODES = JSON.stringify(
  Object.fromEntries(THEME_MODES.map(({ key }) => [key, 1])),
);
const VALID_THEME_PRESETS = JSON.stringify(
  Object.fromEntries(THEME_PRESETS.map(({ key }) => [key, 1])),
);

const BOOTSTRAP = `(function(){try{
var t=localStorage.getItem("mdhub-theme")||"system";
var ms=${VALID_THEME_MODES};
if(!ms[t])t="system";
var p=localStorage.getItem("mdhub-theme-preset")||"paper";
var ps=${VALID_THEME_PRESETS};
if(!ps[p])p="paper";
document.documentElement.dataset.theme=p;
if(t==="dark"||(t==="system"&&window.matchMedia("(prefers-color-scheme: dark)").matches)){
document.documentElement.classList.add("dark");}
var f=localStorage.getItem("mdhub-font")||"system";
var fonts={system:1,serif:1,kai:1,hei:1,wenkai:1,fangsong:1};
if(!fonts[f])f="system";
document.documentElement.classList.add("font-"+f);
var sizes={sm:"0.9375rem",md:"1.0625rem",lg:"1.1875rem",xl:"1.3125rem"};
var s=sizes[localStorage.getItem("mdhub-font-size")||""];
if(s)document.documentElement.style.setProperty("--reader-font-size",s);
var widths={narrow:"36rem",normal:"42rem",wide:"56rem",full:"none"};
var w=widths[localStorage.getItem("mdhub-width")||""];
if(w)document.documentElement.style.setProperty("--reader-width",w);
}catch(e){}})();`;

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en" suppressHydrationWarning>
      <head>
        {/* On-demand webfont for the .font-wenkai reader preset (LXGW WenKai) */}
        <link
          rel="stylesheet"
          href="https://cdn.jsdelivr.net/npm/lxgw-wenkai-webfont@1.7.0/style.css"
        />
        {/* Kept outside PostCSS until its parser accepts Custom Highlight syntax. */}
        <link rel="stylesheet" href="/mdhub/custom-highlights.css" />
      </head>
      <body className="min-h-screen bg-white text-stone-900 antialiased">
        <script dangerouslySetInnerHTML={{ __html: BOOTSTRAP }} />
        <FontProvider>{children}</FontProvider>
      </body>
    </html>
  );
}
