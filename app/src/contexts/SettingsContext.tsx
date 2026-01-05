import { createContext, useContext, ReactNode } from 'react';

interface SettingsContextValue {
  settings: Record<string, string>;
  setSetting: (key: string, value: string) => void;
}

const SettingsContext = createContext<SettingsContextValue | null>(null);

interface SettingsProviderProps {
  children: ReactNode;
  settings: Record<string, string>;
  setSetting: (key: string, value: string) => void;
}

export function SettingsProvider({ children, settings, setSetting }: SettingsProviderProps) {
  return (
    <SettingsContext.Provider value={{ settings, setSetting }}>
      {children}
    </SettingsContext.Provider>
  );
}

export function useSettings(): SettingsContextValue {
  const context = useContext(SettingsContext);
  if (!context) {
    // Return a safe fallback when used outside provider (e.g., during initial render)
    return {
      settings: {},
      setSetting: () => {},
    };
  }
  return context;
}
