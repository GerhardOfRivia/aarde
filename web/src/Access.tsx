import { useCallback, useEffect, useState } from "react";
import type { ReactNode } from "react";
import { Alert, Button, CircularProgress, TextField } from "@mui/material";
import { APIError, request } from "./types";
import type { AccessInfo } from "./types";
import { useAppTheme } from "./theme";
import type { ThemePreference } from "./theme";

const tokenKey = "aarde.web.token";
const rejectedMessage = "That token was not accepted. Read the current token file and try again.";

function storedToken() {
  try { return sessionStorage.getItem(tokenKey) ?? ""; }
  catch { return ""; }
}
function persistToken(token: string) {
  try {
    if (token) sessionStorage.setItem(tokenKey, token);
    else sessionStorage.removeItem(tokenKey);
  } catch { /* Authentication still works when browser storage is unavailable. */ }
}

export function ThemeControl() {
  const { preference, setPreference } = useAppTheme();
  return <label className="theme-control">
    <span>Theme</span>
    <select aria-label="Color theme" value={preference}
      onChange={(event) => setPreference(event.target.value as ThemePreference)}>
      <option value="system">Auto</option><option value="light">Light</option><option value="dark">Night</option>
    </select>
  </label>;
}

export interface Session {
  token: string;
  info: AccessInfo;
  message: string;
  dismissMessage(): void;
  signIn(): void;
  signOut(message?: string): void;
  unauthorized(): void;
}

export function AccessGate({ children }: { children(session: Session): ReactNode }) {
  const [token, setToken] = useState(storedToken);
  const [info, setInfo] = useState<AccessInfo | null>(null);
  const [checking, setChecking] = useState(true);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [signingIn, setSigningIn] = useState(false);
  const [value, setValue] = useState("");

  const signOut = useCallback((reason = "") => {
    persistToken("");
    setToken("");
    setValue("");
    setInfo(null);
    setChecking(true);
    setSigningIn(false);
    setMessage(reason);
    setAttempt((n) => n + 1);
  }, []);
  const unauthorized = useCallback(() => signOut(rejectedMessage), [signOut]);

  useEffect(() => {
    const controller = new AbortController();
    setChecking(true);
    setError("");
    request<AccessInfo>("/api/v1/info", token, { signal: controller.signal })
      .then((access) => {
        if (controller.signal.aborted) return;
        persistToken(token);
        setInfo(access);
        setChecking(false);
      })
      .catch((reason: unknown) => {
        if (controller.signal.aborted) return;
        if (reason instanceof APIError && reason.status === 401) {
          if (token) { unauthorized(); return; }
          setInfo(null);
        } else {
          setError(reason instanceof Error ? reason.message : "Could not connect to Aarde.");
        }
        setChecking(false);
      });
    return () => controller.abort();
  }, [token, attempt, unauthorized]);

  if (info && !checking && !error && !signingIn) {
    return children({ token, info, message, signOut, unauthorized,
      dismissMessage: () => setMessage(""), signIn: () => { setValue(""); setSigningIn(true); } });
  }
  return <div className="app">
    <header className="header">
      <a className="brand" href="/" aria-label="Aarde home"><img className="brand-mark" src="/icon.png" alt="" width={42} height={42} />aarde</a>
      <div className="header-actions"><Button className="access-button" size="small" href="/docs/">API docs</Button><ThemeControl /></div>
    </header>
    <main className="auth-shell">
      <section className="auth-card" aria-labelledby="auth-title">
        <span className="eyebrow">LOCAL IMAGERY · SPATIAL DISCOVERY</span>
        <h1 id="auth-title">{checking ? "Connecting…" : error ? "Connection unavailable" : "Open your catalog"}</h1>
        {checking ? <CircularProgress size={26} aria-label="Checking access" /> : error ? <>
          <Alert severity="error">{error}</Alert>
          <Button variant="contained" onClick={() => setAttempt((n) => n + 1)}>Retry</Button>
        </> : <>
          <p>Paste the access token from the private file named in the <code>aarde serve</code> startup log. It stays in this browser tab’s session.</p>
          <form onSubmit={(event) => {
            event.preventDefault();
            if (!value.trim()) return;
            setToken(value.trim());
            setValue("");
            setInfo(null);
            setChecking(true);
            setSigningIn(false);
            setMessage("");
            setAttempt((n) => n + 1);
          }}>
            <TextField label="Web access token" type="password" autoComplete="off" autoFocus fullWidth
              value={value} onChange={(event) => setValue(event.target.value)}
              slotProps={{ htmlInput: { spellCheck: false } }} />
            {message && <Alert severity="warning">{message}</Alert>}
            <Button type="submit" variant="contained" disabled={!value.trim()}>Open catalog</Button>
            {info?.public_read && <Button onClick={() => setSigningIn(false)}>Back to read-only view</Button>}
          </form>
          <p className="auth-note">Use HTTPS or a trusted local connection to protect the token in transit.</p>
        </>}
      </section>
    </main>
  </div>;
}
