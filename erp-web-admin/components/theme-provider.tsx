"use client";

import { ThemeProvider as NextThemesProvider } from "next-themes";
import type { ComponentProps } from "react";

// Light by default so nobody's screen changes under them on upgrade; dark
// and "follow the OS" are one click away in the top bar.
export function ThemeProvider(props: ComponentProps<typeof NextThemesProvider>) {
  return <NextThemesProvider attribute="class" defaultTheme="light" enableSystem disableTransitionOnChange {...props} />;
}
