"use client";

import type { Dispatch, ReactNode, SetStateAction } from "react";
import { createContext, useContext, useEffect, useState } from "react";
import { usePathname } from "next/navigation";

import Nav from "./nav";

export const APP_RAIL_EXTRA_ID = "app-rail-extra";

type AppChromeContextValue = {
  railCollapsed: boolean;
  setRailCollapsed: Dispatch<SetStateAction<boolean>>;
};

const AppChromeContext = createContext<AppChromeContextValue | null>(null);

type AppChromeProps = {
  children: ReactNode;
};

export function useAppChrome() {
  const context = useContext(AppChromeContext);

  if (!context) {
    throw new Error("useAppChrome 必须在 AppChrome 内部使用。");
  }

  return context;
}

export function AppChrome({ children }: AppChromeProps) {
  const pathname = usePathname();
  const [railCollapsed, setRailCollapsed] = useState(false);

  useEffect(() => {
    if (pathname !== "/") {
      setRailCollapsed(false);
    }
  }, [pathname]);

  return (
    <AppChromeContext.Provider
      value={{
        railCollapsed,
        setRailCollapsed
      }}
    >
      <div className="app-body" data-rail-collapsed={railCollapsed}>
        <aside className="app-rail" data-collapsed={railCollapsed}>
          {railCollapsed ? null : <Nav extraSlotId={APP_RAIL_EXTRA_ID} variant="sidebar" />}
        </aside>

        <main className="app-main">{children}</main>
      </div>
    </AppChromeContext.Provider>
  );
}
