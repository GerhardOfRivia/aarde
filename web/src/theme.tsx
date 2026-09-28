import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useState,
} from "react";
import type { ReactNode } from "react";
import { createTheme, CssBaseline, ThemeProvider } from "@mui/material";

export type ThemePreference = "system" | "light" | "dark";
type ThemeMode = "light" | "dark";
const storageKey = "aarde.web.theme";

function readPreference(): ThemePreference {
  try {
    const value = localStorage.getItem(storageKey);
    if (value === "light" || value === "dark") return value;
  } catch {
    // Theme switching still works when browser storage is unavailable.
  }
  return "system";
}

function systemIsDark() {
  return (
    typeof matchMedia === "function" &&
    matchMedia("(prefers-color-scheme: dark)").matches
  );
}

interface ThemeSettings {
  preference: ThemePreference;
  mode: ThemeMode;
  setPreference(preference: ThemePreference): void;
}

const ThemeContext = createContext<ThemeSettings | null>(null);

export function useAppTheme() {
  const value = useContext(ThemeContext);
  if (!value) throw new Error("useAppTheme requires AppThemeProvider");
  return value;
}

export function AppThemeProvider({ children }: { children: ReactNode }) {
  const [preference, setStoredPreference] =
    useState<ThemePreference>(readPreference);
  const [systemDark, setSystemDark] = useState(systemIsDark);
  const mode: ThemeMode =
    preference === "system" ? (systemDark ? "dark" : "light") : preference;

  useEffect(() => {
    if (typeof matchMedia !== "function") return;
    const query = matchMedia("(prefers-color-scheme: dark)");
    const sync = () => setSystemDark(query.matches);
    sync();
    query.addEventListener("change", sync);
    return () => query.removeEventListener("change", sync);
  }, []);

  useEffect(() => {
    const sync = (event: StorageEvent) => {
      if (event.key === storageKey || event.key === null)
        setStoredPreference(readPreference());
    };
    window.addEventListener("storage", sync);
    return () => window.removeEventListener("storage", sync);
  }, []);

  useLayoutEffect(() => {
    document.documentElement.dataset.theme = mode;
    const meta = document.querySelector<HTMLMetaElement>(
      'meta[name="theme-color"]',
    );
    if (meta) meta.content = mode === "dark" ? "#0f1311" : "#f4f5f2";
  }, [mode]);

  const setPreference = useCallback((value: ThemePreference) => {
    try {
      localStorage.setItem(storageKey, value);
    } catch {
      /* Private browser storage may be unavailable. */
    }
    setStoredPreference(value);
  }, []);

  const theme = useMemo(
    () =>
      createTheme({
        palette: {
          mode,
          primary: {
            main: mode === "dark" ? "#b8ef68" : "#1f6b5d",
            contrastText: mode === "dark" ? "#14200f" : "#ffffff",
          },
          background: {
            default: mode === "dark" ? "#0f1311" : "#f4f5f2",
            paper: mode === "dark" ? "#171c19" : "#ffffff",
          },
          text: {
            primary: mode === "dark" ? "#edf2ed" : "#243632",
            secondary: mode === "dark" ? "#aab3ab" : "#64766c",
          },
          divider: mode === "dark" ? "#3b443f" : "#d4ded7",
        },
        typography: {
          fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif",
          button: { textTransform: "none", fontWeight: 600 },
        },
        shape: { borderRadius: 7 },
        components: {
          MuiButton: { defaultProps: { disableElevation: true } },
          MuiPaper: { styleOverrides: { root: { backgroundImage: "none" } } },
        },
      }),
    [mode],
  );

  return (
    <ThemeContext.Provider value={{ preference, mode, setPreference }}>
      <ThemeProvider theme={theme}>
        <CssBaseline />
        {children}
      </ThemeProvider>
    </ThemeContext.Provider>
  );
}
