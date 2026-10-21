import { useEffect, useState, type FormEvent } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { Loader2 } from "lucide-react";
import { ApiError, api } from "../lib/api";
import { Button } from "../components/ui/button";
import { Field, Input } from "../components/ui/controls";
import { logoUrl, loginMessage, productName, useSettings } from "../lib/settings";

export function Login() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [setupNeeded, setSetupNeeded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [sso, setSso] = useState(false);
  const [hosted, setHosted] = useState(false);
  const [registration, setRegistration] = useState(false);
  const [mode, setMode] = useState<"signin" | "register">("signin");
  const [orgName, setOrgName] = useState("");
  const navigate = useNavigate();
  const location = useLocation();
  const came = (location.state ?? {}) as { from?: { pathname?: string }; notice?: string | null };
  const { settings, reload } = useSettings();
  const name = productName(settings);
  const logo = logoUrl(settings);
  const message = loginMessage(settings);

  useEffect(() => {
    api
      .oidcStatus()
      .then((s) => setSso(s.enabled))
      .catch(() => setSso(false));
    api
      .hosted()
      .then((h) => {
        setHosted(h.hosted);
        setRegistration(h.registration);
      })
      .catch(() => {
        setHosted(false);
        setRegistration(false);
      });
  }, []);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      if (mode === "register") {
        await api.register({ email, password, org_name: orgName || email });
      } else {
        await api.login(email, password);
      }
      // Settings loaded before sign-in are the public branding fallback.
      // Load the signed-in ones before showing the app.
      await reload();
      // Back to the page the session ended on. Only a plain in-app path:
      // this value came through the router and must not lead anywhere else.
      const back = came.from?.pathname ?? "/";
      navigate(/^\/(?![\/\\])[^\\]*$/.test(back) ? back : "/", { replace: true });
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 401 && err.message.includes("setup")) {
          setSetupNeeded(true);
        } else {
          setError(err.message === "invalid credentials" ? "Wrong email or password." : err.message);
        }
      } else {
        setError("Cannot reach server.");
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex min-h-screen bg-background">
      <BrandPanel name={name} logo={logo} message={message} />

      <main className="relative flex flex-1 items-center justify-center px-6 py-12">
        <div className="animate-fade-in-up w-full max-w-[380px]">
          <div className="mb-8 lg:hidden">
            <BrandMark name={name} logo={logo} compact />
            <p className="mt-2 text-sm text-muted-foreground">{message}</p>
          </div>

          <h2 className="font-brand text-xl tracking-tight">
            {mode === "register" ? "Create account" : "Sign in"}
          </h2>
          <p className="mt-1 text-sm text-muted-foreground">
            {mode === "register"
              ? "A new organization on this Argus cluster."
              : "Watch your infrastructure from here."}
          </p>

          <form onSubmit={submit} aria-busy={loading} className="surface mt-6 flex flex-col gap-4 p-6">
            {mode === "register" && (
              <Field label="Organization">
                <Input
                  value={orgName}
                  onChange={(e) => setOrgName(e.target.value)}
                  placeholder="Acme"
                  required
                />
              </Field>
            )}
            <Field label="Email">
              <Input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="admin@example.com"
                autoComplete="username"
                required
                autoFocus
              />
            </Field>
            <Field label="Password">
              <Input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
                autoComplete="current-password"
                required
              />
            </Field>
            {came.notice && !error && (
              <p role="status" className="mb-4 rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-sm">
                {came.notice}
              </p>
            )}
            {error && (
              <p role="alert" className="rounded-lg border border-danger/30 bg-danger/10 px-3 py-2 text-xs font-medium text-danger">
                {error}
              </p>
            )}
            {setupNeeded && (
              <p role="status" className="rounded-lg border border-warning/30 bg-warning/10 px-3 py-2 text-xs text-warning">
                No admin account exists yet. Set <code>ARGUS_ADMIN_EMAIL</code> and{" "}
                <code>ARGUS_ADMIN_PASSWORD</code> environment variables on the
                api service and restart the stack.
              </p>
            )}
            <Button type="submit" disabled={loading} className="mt-1 h-10 w-full">
              {loading ? (
                <Loader2 size={16} className="animate-spin" />
              ) : mode === "register" ? (
                "Create account"
              ) : (
                "Sign in"
              )}
            </Button>
            {hosted && registration && (
              <button
                type="button"
                className="text-sm text-muted-foreground underline-offset-4 hover:underline"
                onClick={() => {
                  setMode(mode === "register" ? "signin" : "register");
                  setError(null);
                }}
              >
                {mode === "register" ? "Already have an account? Sign in" : "Create an account"}
              </button>
            )}
            {sso && (
              <a
                href="/api/auth/oidc/start"
                className="inline-flex h-10 w-full items-center justify-center rounded-md border border-border text-sm font-medium hover:bg-muted"
              >
                Sign in with SSO
              </a>
            )}
          </form>
        </div>
      </main>
    </div>
  );
}

function BrandPanel({
  name,
  logo,
  message,
}: {
  name: string;
  logo: string | null;
  message: string;
}) {
  return (
    <aside className="relative hidden min-h-screen w-[54%] overflow-hidden bg-[#0c0d0f] text-white lg:flex lg:flex-col">
      <div className="pointer-events-none absolute -left-24 top-[-120px] h-[520px] w-[520px] rounded-full bg-primary/25 blur-[140px]" />
      <div className="pointer-events-none absolute bottom-[-80px] right-[-60px] h-[380px] w-[380px] rounded-full bg-primary/15 blur-[120px]" />
      <LoginGraphic />
      <div className="relative z-10 mt-auto p-12">
        <BrandMark name={name} logo={logo} />
        <p className="mt-3 max-w-sm text-sm text-white/65">{message}</p>
      </div>
    </aside>
  );
}

function BrandMark({
  name,
  logo,
  compact,
}: {
  name: string;
  logo: string | null;
  compact?: boolean;
}) {
  return (
    <div className="flex items-center gap-3">
      {logo ? (
        <img
          src={logo}
          alt=""
          className={compact ? "h-9 w-9 object-contain" : "h-11 w-11 object-contain"}
        />
      ) : (
        <span className={`flex items-center justify-center text-primary ${compact ? "h-9 w-9" : "h-11 w-11"}`}>
          <RadarMark size={compact ? 22 : 28} />
        </span>
      )}
      <h1 className={`font-brand tracking-tight ${compact ? "text-xl" : "text-3xl"}`}>{name}</h1>
    </div>
  );
}

function RadarMark({ size }: { size: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M19.07 4.93A10 10 0 0 0 6.99 3.34" />
      <path d="M4 6h.01" />
      <path d="M2.29 9.62a10 10 0 1 0 19.42-3.34" />
      <path d="M16.24 7.76A6 6 0 0 0 8.23 6.77" />
      <path d="M12 10v2" />
      <path d="m16.24 16.24-2.12-4.24" />
    </svg>
  );
}

function LoginGraphic() {
  const line =
    "M40 430 C 90 410, 120 360, 170 370 S 250 420, 300 350 S 390 260, 460 280 S 560 360, 640 240 S 720 180, 800 210";
  const area = `${line} L800 480 L40 480 Z`;

  return (
    <svg
      className="pointer-events-none absolute inset-0 h-full w-full"
      viewBox="0 0 800 560"
      preserveAspectRatio="xMidYMid slice"
      aria-hidden="true"
    >
      <defs>
        <linearGradient id="login-fill" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="var(--chart-line)" stopOpacity="0.45" />
          <stop offset="100%" stopColor="var(--chart-line)" stopOpacity="0" />
        </linearGradient>
        <radialGradient id="login-sweep" cx="50%" cy="50%" r="50%">
          <stop offset="0%" stopColor="var(--primary)" stopOpacity="0.35" />
          <stop offset="100%" stopColor="var(--primary)" stopOpacity="0" />
        </radialGradient>
      </defs>

      {[90, 160, 230, 300].map((r) => (
        <circle
          key={r}
          cx="400"
          cy="280"
          r={r}
          fill="none"
          stroke="currentColor"
          strokeOpacity="0.08"
          strokeWidth="1"
        />
      ))}
      <line x1="400" y1="0" x2="400" y2="560" stroke="currentColor" strokeOpacity="0.05" />
      <line x1="0" y1="280" x2="800" y2="280" stroke="currentColor" strokeOpacity="0.05" />

      <g transform="translate(400 280)">
        <g className="radar-sweep">
          <path d="M0 0 L0 -260 A260 260 0 0 1 220 -140 Z" fill="url(#login-sweep)" />
        </g>
      </g>

      <circle cx="400" cy="280" r="4" fill="var(--primary)" />
      <circle cx="400" cy="280" r="10" fill="none" stroke="var(--primary)" strokeOpacity="0.5" />

      <path d={area} fill="url(#login-fill)" />
      <path
        d={line}
        fill="none"
        stroke="var(--chart-line)"
        strokeWidth="2.2"
        pathLength="1"
        className="login-draw"
      />
    </svg>
  );
}
