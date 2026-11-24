import { ClerkProvider, SignIn, SignUp, UserButton, useAuth } from "@clerk/react";
import { createContext, useContext, useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { api, message } from "./api";
import type { Credential } from "./api";

export type Principal = { user_id: string; role: "customer" | "admin" | "owner" };
type Auth = { principal?: Principal; loading: boolean; error?: unknown; customer: Credential; admin: Credential };
const AuthContext = createContext<Auth>({ loading: true, customer: "", admin: "" });
export const useSession = () => useContext(AuthContext);
export const demoMode = import.meta.env.VITE_AUTH_MODE === "demo";
const publishableKey = import.meta.env.VITE_CLERK_PUBLISHABLE_KEY;

function Session({ children }: { children: ReactNode }) {
  const { isLoaded, userId, getToken } = useAuth();
  const current = useRef({ userId, getToken });
  current.current = { userId, getToken };
  const token = useMemo<Credential>(() => userId ? {
    userID: userId,
    getToken: () => current.current.userId === userId ? current.current.getToken() : Promise.resolve(null),
  } : "", [userId]);
  const [result, setResult] = useState<{ id?: string | null; principal?: Principal; error?: unknown }>({});
  useEffect(() => {
    if (!isLoaded || !userId) return;
    const controller = new AbortController();
    async function load() {
      try {
        const principal = await api<Principal>("/api/auth/me", { token, signal: controller.signal });
        if (!controller.signal.aborted) setResult({ id: userId, principal });
      } catch (error) {
        if (!controller.signal.aborted) setResult({ id: userId, error });
      }
    }
    void load();
    const timer = window.setInterval(load, 30_000);
    return () => { controller.abort(); window.clearInterval(timer); };
  }, [isLoaded, userId, token]);
  const principal = userId && result.id === userId ? result.principal : undefined;
  return <AuthContext.Provider value={{ principal, loading: !isLoaded || Boolean(userId && result.id !== userId),
    error: userId && result.id === userId ? result.error : undefined, customer: principal ? token : "", admin: principal ? token : "" }}>
    {children}
  </AuthContext.Provider>;
}

type DemoTokens = { customer: string; admin: string };
const DemoContext = createContext<{ tokens: DemoTokens; setTokens: (tokens: DemoTokens) => void }>({ tokens: { customer: "", admin: "" }, setTokens: () => {} });
function DemoSession({ children }: { children: ReactNode }) {
  const [tokens, setTokens] = useState<DemoTokens>(() => {
    try { return JSON.parse(sessionStorage.getItem("flashkart-demo") || "null") || { customer: "", admin: "" }; }
    catch { return { customer: "", admin: "" }; }
  });
  const [result, setResult] = useState<{ token?: string; principal?: Principal; error?: unknown }>({});
  const token = tokens.admin || tokens.customer;
  useEffect(() => {
    sessionStorage.setItem("flashkart-demo", JSON.stringify(tokens));
    if (!token) return;
    const controller = new AbortController();
    api<Principal>("/api/auth/me", { token, signal: controller.signal }).then(
      principal => { if (!controller.signal.aborted) setResult({ token, principal }); },
      error => { if (!controller.signal.aborted) setResult({ token, error }); },
    );
    return () => controller.abort();
  }, [token, tokens]);
  const principal = token && result.token === token ? result.principal : undefined;
  return <DemoContext.Provider value={{ tokens, setTokens }}><AuthContext.Provider value={{ principal,
    loading: Boolean(token && result.token !== token), error: result.token === token ? result.error : undefined,
    customer: principal ? tokens.customer || tokens.admin : "", admin: principal ? tokens.admin : "" }}>
    {children}
  </AuthContext.Provider></DemoContext.Provider>;
}

export function Authentication({ children }: { children: ReactNode }) {
  if (demoMode) return <DemoSession>{children}</DemoSession>;
  if (!publishableKey) return <main className="shell main"><h1>Flashkart sign-in setup</h1>
    <p>Configure the Clerk publishable key and restart the development server.</p></main>;
  return <ClerkProvider publishableKey={publishableKey} signInUrl="/login" signUpUrl="/sign-up"
    signInFallbackRedirectUrl="/" signUpFallbackRedirectUrl="/" afterSignOutUrl="/"
    appearance={{ variables: { colorPrimary: "#5b3df5", borderRadius: "0.75rem" } }}>
    <Session>{children}</Session>
  </ClerkProvider>;
}

export function AccountControls() {
  const auth = useSession();
  const { setTokens } = useContext(DemoContext);
  if (!auth.principal) return null;
  return <div className="account-controls"><span className="pill">{auth.principal.role}</span>
    {demoMode ? <button className="secondary" onClick={() => setTokens({ customer: "", admin: "" })}>Sign out</button> : <UserButton />}
  </div>;
}

export function returnDestination(value: string | null) {
  if (!value || !value.startsWith("/") || value.startsWith("//") || value.includes("\\")) return "/";
  const target = new URL(value, window.location.origin);
  if (target.origin !== window.location.origin || !["/", "/activity", "/activity/cart", "/analytics", "/admin", "/admin/sales", "/admin/rush", "/replicas", "/team"].includes(target.pathname)) return "/";
  return target.pathname + target.search;
}

export function Login({ signUp = false }: { signUp?: boolean }) {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const auth = useSession();
  const { tokens, setTokens } = useContext(DemoContext);
  const [draft, setDraft] = useState(tokens);
  const [submitted, setSubmitted] = useState(false);
  const destination = returnDestination(params.get("returnTo"));
  useEffect(() => { if (auth.principal && (!demoMode || submitted)) navigate(destination, { replace: true }); }, [auth.principal, destination, navigate, submitted]);
  function submit(event: FormEvent) { event.preventDefault(); setSubmitted(true); setTokens({ customer: draft.customer.trim(), admin: draft.admin.trim() }); }
  return <section className="auth-page"><div className="auth-intro"><span className="eyebrow">YOUR NEXT GREAT FIND</span>
    <h1>{signUp ? "Welcome to Flashkart." : "Good to see you again."}</h1>
    <p>Sign in to reserve your favourite drops and keep track of every purchase.</p>
  </div><div className="auth-card">
    {demoMode ? <form className="panel stack" onSubmit={submit}><h2>Local demo access</h2><p>Use the tokens configured on your local backend.</p>
      <label>Customer token<input type="password" autoComplete="off" value={draft.customer} onChange={e => setDraft({ ...draft, customer: e.target.value })} /></label>
      <label>Administrator token<input type="password" autoComplete="off" value={draft.admin} onChange={e => setDraft({ ...draft, admin: e.target.value })} /></label>
      <button disabled={auth.loading || !(draft.customer.trim() || draft.admin.trim())}>Sign in</button>
      {auth.error !== undefined && <p role="alert">{message(auth.error)}</p>}
    </form> : signUp
      ? <SignUp routing="path" path="/sign-up" signInUrl="/login" fallbackRedirectUrl={destination} />
      : <SignIn routing="path" path="/login" signUpUrl="/sign-up" fallbackRedirectUrl={destination} />}
  </div></section>;
}
