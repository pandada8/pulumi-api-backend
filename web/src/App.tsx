import { useEffect, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Link,
  Navigate,
  Outlet,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from "react-router-dom";
import {
  ArrowRight,
  ChevronRight,
  CircleHelp,
  Folder,
  Layers3,
  LockKeyhole,
  LogOut,
  Menu,
  PanelLeftClose,
  ShieldCheck,
} from "lucide-react";
import { request } from "./lib/api";
import { useStacks } from "./lib/queries";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Badge } from "./components/ui/badge";
import { Loading, Logo } from "./components/common";
import { StackTree } from "./components/stack-tree";
import { StackPage } from "./components/stack-detail";

type Session = { login: string; name: string };
export default function App() {
  const client = useQueryClient();
  const session = useQuery({
    queryKey: ["session"],
    queryFn: ({ signal }) =>
      request<Session>("/session", { signal }).then((r) => r.data),
    retry: false,
    staleTime: 30_000,
  });
  useEffect(() => {
    const clear = () => {
      client.setQueryData(["session"], null);
      client.removeQueries({ predicate: (q) => q.queryKey[0] !== "session" });
    };
    window.addEventListener("pulumid:unauthorized", clear);
    return () => window.removeEventListener("pulumid:unauthorized", clear);
  }, [client]);
  return (
    <Routes>
      <Route path="/login" element={<Login authenticated={!!session.data} />} />
      <Route
        element={
          session.isPending ? (
            <div className="app-loading">
              <Logo />
              <Loading />
            </div>
          ) : session.data ? (
            <Shell session={session.data} />
          ) : (
            <LoginRedirect />
          )
        }
      >
        <Route index element={<Navigate to="/stacks" replace />} />
        <Route path="stacks" element={<StackTree />} />
        <Route path=":org/:project/:stack" element={<StackPage />} />
        <Route path=":org/:project/:stack/:section" element={<StackPage />} />
        <Route
          path=":org/:project/:stack/:section/:version"
          element={<StackPage />}
        />
        <Route
          path="*"
          element={
            <div className="empty-state">
              <h1>Page not found</h1>
              <p>This page doesn’t exist in your workspace.</p>
              <Button asChild>
                <Link to="/stacks">Back to stacks</Link>
              </Button>
            </div>
          }
        />
      </Route>
    </Routes>
  );
}
function LoginRedirect() {
  const location = useLocation();
  return (
    <Navigate
      to="/login"
      state={{ from: location.pathname + location.search }}
      replace
    />
  );
}
function Shell({ session }: { session: Session }) {
  const { stacks } = useStacks();
  const [mobile, setMobile] = useState(false);
  const [logoutError, setLogoutError] = useState("");
  const [signingOut, setSigningOut] = useState(false);
  const location = useLocation(),
    navigate = useNavigate(),
    client = useQueryClient();
  const projects = [...new Set(stacks.map((s) => s.projectName))].sort();
  const isStacks = location.pathname === "/stacks";
  useEffect(() => {
    setMobile(false);
  }, [location.pathname, location.search]);
  useEffect(() => {
    function key(e: KeyboardEvent) {
      if (
        e.key === "/" &&
        !(e.target instanceof HTMLInputElement) &&
        !(e.target instanceof HTMLTextAreaElement)
      ) {
        const input = document.querySelector<HTMLInputElement>(
          '[aria-label="Search stacks"]',
        );
        if (input) {
          e.preventDefault();
          input.focus();
        }
      }
      if (e.key === "Escape") setMobile(false);
    }
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, []);
  async function logout() {
    setSigningOut(true);
    setLogoutError("");
    try {
      const { data } = await request<{ csrfToken: string }>(
        "/session/refresh",
        { method: "POST" },
      );
      await request("/session", {
        method: "DELETE",
        headers: { "X-CSRF-Token": data.csrfToken },
      });
      client.clear();
      navigate("/login", { replace: true });
    } catch (e) {
      setLogoutError(e instanceof Error ? e.message : "Sign out failed");
    } finally {
      setSigningOut(false);
    }
  }
  const crumbs = location.pathname
    .split("/")
    .filter(Boolean)
    .map((s) => decodeURIComponent(s));
  return (
    <div className="app-layout">
      <a className="skip-link" href="#main-content">
        Skip to content
      </a>
      {mobile && (
        <button
          className="sidebar-backdrop"
          aria-label="Close navigation"
          onClick={() => setMobile(false)}
        />
      )}
      <aside
        className={"sidebar " + (mobile ? "mobile-open" : "")}
        aria-label="Workspace navigation"
      >
        <Link to="/stacks" className="brand">
          <Logo />
          <strong>pulumid</strong>
          <span className="brand-label">console</span>
        </Link>
        <div className="workspace-switch">
          <span className="workspace-avatar">
            <Layers3 className="size-4" />
          </span>
          <div>
            <strong>Infrastructure</strong>
            <span>Your workspace</span>
          </div>
          <Badge variant="outline">SELF-HOSTED</Badge>
        </div>
        <div className="sidebar-section-label">WORKSPACE</div>
        <Link to="/stacks" className="sidebar-nav active">
          <Layers3 className="size-4" />
          <span>Stacks</span>
          <span className="nav-count">{stacks.length || "—"}</span>
        </Link>
        <div className="sidebar-section-label project-label">
          PROJECTS<span>{projects.length}</span>
        </div>
        <nav className="project-nav" aria-label="Projects">
          {projects.slice(0, 8).map((p) => (
            <Link
              key={p}
              to={"/stacks?project=" + encodeURIComponent(p)}
              className={
                new URLSearchParams(location.search).get("project") === p
                  ? "selected"
                  : ""
              }
            >
              <Folder className="size-3.5" />
              <span>{p}</span>
              <span className="project-nav-count">
                {stacks.filter((s) => s.projectName === p).length}
              </span>
            </Link>
          ))}
          {projects.length > 8 && (
            <Link className="more-projects" to="/stacks">
              View all {projects.length} projects
              <ArrowRight className="size-3" />
            </Link>
          )}
          {!projects.length && (
            <span className="sidebar-empty">
              Your projects will appear here.
            </span>
          )}
        </nav>
        <div className="sidebar-bottom">
          <div className="sidebar-help">
            <CircleHelp className="size-4" />
            <span>Manage with the Pulumi CLI</span>
          </div>
          <div className="user-row">
            <span className="user-avatar">
              {(session.name || session.login).slice(0, 2).toUpperCase()}
            </span>
            <div>
              <strong>{session.name || session.login}</strong>
              <span>Workspace member</span>
            </div>
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label="Sign out"
              disabled={signingOut}
              onClick={() => void logout()}
            >
              <LogOut className="size-4" />
            </Button>
          </div>
          {logoutError && (
            <p className="sidebar-error" role="alert">
              {logoutError}
            </p>
          )}
        </div>
      </aside>
      <div className="main-layout">
        <header className="topbar">
          <div className="breadcrumbs">
            <Button
              size="icon-sm"
              variant="ghost"
              className="mobile-toggle"
              aria-label="Open navigation"
              onClick={() => setMobile(true)}
            >
              <Menu className="size-4" />
            </Button>
            <PanelLeftClose className="size-4 desktop-label text-muted-foreground" />
            <span className="breadcrumb-divider" />
            <Link to="/stacks">Workspace</Link>
            <ChevronRight className="size-3" />
            <span>{isStacks ? "Stacks" : crumbs.at(-1) || "Stacks"}</span>
          </div>
          <div className="topbar-right">
            <span className="read-only">
              <LockKeyhole className="size-3" />
              Read-only console
            </span>
            <span className="connection-status">
              <span className="live-dot" />
              Session active
            </span>
          </div>
        </header>
        <main id="main-content" className="main-content">
          <Outlet />
        </main>
        <footer className="app-footer">
          <span>
            pulumid <span className="footer-slash">/</span> Infrastructure
            state, in your hands.
          </span>
          <span>
            <ShieldCheck className="size-3" />
            Self-hosted & private
          </span>
        </footer>
      </div>
    </div>
  );
}
function Login({ authenticated }: { authenticated: boolean }) {
  const [token, setToken] = useState(""),
    [error, setError] = useState(""),
    [loading, setLoading] = useState(false);
  const client = useQueryClient(),
    navigate = useNavigate(),
    location = useLocation();
  if (authenticated) return <Navigate to="/stacks" replace />;
  async function submit(e: FormEvent) {
    e.preventDefault();
    setLoading(true);
    setError("");
    try {
      await request("/session", {
        method: "POST",
        body: JSON.stringify({ token }),
      });
      setToken("");
      await client.invalidateQueries({ queryKey: ["session"] });
      const from = (location.state as { from?: string } | null)?.from;
      navigate(
        from?.startsWith("/") && !from.startsWith("//") && from !== "/login"
          ? from
          : "/stacks",
        { replace: true },
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : "Sign in failed");
    } finally {
      setLoading(false);
    }
  }
  return (
    <div className="login-layout">
      <div className="login-main">
        <Link to="/login" className="brand">
          <Logo />
          <strong>pulumid</strong>
          <span className="brand-label">console</span>
        </Link>
        <div className="login-form-wrap">
          <span className="login-icon">
            <Layers3 className="size-6" />
          </span>
          <div className="eyebrow">WELCOME TO YOUR WORKSPACE</div>
          <h1>
            Infrastructure.
            <br />A little more organized.
          </h1>
          <p>Sign in to explore your stacks, resources, and update history.</p>
          <form onSubmit={(e) => void submit(e)}>
            <label htmlFor="token">API token</label>
            <Input
              id="token"
              type="password"
              autoComplete="off"
              placeholder="Enter your Pulumi API token"
              required
              value={token}
              onChange={(e) => setToken(e.target.value)}
            />
            {error && (
              <div role="alert" className="form-error">
                {error}
              </div>
            )}
            <Button
              className="login-button"
              type="submit"
              disabled={loading || !token.trim()}
            >
              {loading ? "Signing in…" : "Sign in"}
              <ArrowRight className="size-4" />
            </Button>
          </form>
          <div className="login-privacy">
            <ShieldCheck className="size-4" />
            Your token is exchanged for a secure session.
            <br />
            It is never saved in browser storage.
          </div>
        </div>
        <div className="login-footer">
          Self-hosted. In your control.<span>pulumid</span>
        </div>
      </div>
      <div className="login-art">
        <div className="art-grid" />
        <div className="art-label">
          <span className="live-dot" />
          YOUR INFRASTRUCTURE, AT A GLANCE
        </div>
        <div className="art-tree">
          <div className="art-node art-org">
            <Logo small />
            <strong>Your workspace</strong>
            <Badge variant="outline">Infrastructure</Badge>
          </div>
          <div className="art-branch">
            <div className="art-node">
              <Folder className="size-4" />
              <strong>commerce-platform</strong>
            </div>
            <div className="art-env">
              <div>
                <Layers3 className="size-4" />
                production
                <CheckCircle />
              </div>
              <div>
                <Layers3 className="size-4" />
                staging
                <CheckCircle />
              </div>
              <div>
                <Layers3 className="size-4" />
                development
                <CheckCircle />
              </div>
            </div>
          </div>
        </div>
        <div className="art-caption">
          <h2>
            Every project.
            <br />
            Every environment.
            <br />
            <span>One clear view.</span>
          </h2>
          <p>A quieter place to understand your infrastructure.</p>
        </div>
      </div>
    </div>
  );
}
function CheckCircle() {
  return <span className="art-check">✓</span>;
}
