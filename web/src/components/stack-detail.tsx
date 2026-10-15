import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, NavLink, useParams } from "react-router-dom";
import {
  ArrowLeft,
  ArrowRight,
  ArrowUpRight,
  Box,
  CheckCircle2,
  ChevronDown,
  Clock3,
  GitCommitHorizontal,
  Layers3,
  RefreshCw,
  Search,
  ShieldCheck,
  Terminal,
} from "lucide-react";
import {
  request,
  date,
  number,
  type StackDetail,
  type UpdateDetail,
  type History,
  type Resource,
} from "@/lib/api";
import { Button } from "./ui/button";
import { Badge } from "./ui/badge";
import { Input } from "./ui/input";
import { Empty, ErrorState, JsonView, Loading, Status } from "./common";

export function StackPage() {
  const { org, project, stack, section, version } = useParams();
  const base =
    "/" + [org, project, stack].map((s) => encodeURIComponent(s!)).join("/");
  const query = useQuery({
    queryKey: ["stack", org, project, stack],
    queryFn: ({ signal }) =>
      request<StackDetail>("/resolve" + base, { signal }).then((r) => r.data),
    retry: false,
    refetchInterval: (q) => (q.state.data?.activeUpdate ? 5000 : false),
  });
  if (query.isPending)
    return (
      <>
        <h1 className="sr-only">Stack</h1>
        <Loading label="Loading stack…" />
      </>
    );
  if (query.isError)
    return (
      <>
        <h1 className="sr-only">Stack</h1>
        <ErrorState error={query.error} retry={() => void query.refetch()} />
      </>
    );
  const data = query.data;
  const valid =
    !section ||
    section === "resources" ||
    section === "history" ||
    ((section === "updates" || section === "previews") && !!version);
  if (!valid)
    return (
      <>
        <h1 className="sr-only">Page not found</h1>
        <Empty
          title="Page not found"
          description="Choose a stack view to continue."
        />
      </>
    );
  return (
    <>
      <Link to="/stacks" className="back-link">
        <ArrowLeft className="size-3.5" />
        All stacks
      </Link>
      <div className="page-heading detail-heading">
        <div>
          <div className="eyebrow">
            {data.org} <span>/</span> {data.project}
          </div>
          <h1>
            <span className="detail-icon">
              <Layers3 className="size-6" />
            </span>
            {data.name}
            <Status value={data.status} />
          </h1>
          <p>
            {data.org} / {data.project} / {data.name}
          </p>
        </div>
        <Button
          variant="outline"
          onClick={() => void query.refetch()}
          disabled={query.isFetching}
        >
          <RefreshCw
            className={query.isFetching ? "size-4 animate-spin" : "size-4"}
          />
          Refresh
        </Button>
      </div>
      <nav className="detail-tabs" aria-label="Stack views">
        {[
          { path: "", name: "Overview", icon: Layers3 },
          { path: "/resources", name: "Resources", icon: Box },
          { path: "/history", name: "History", icon: Clock3 },
        ].map(({ path, name, icon: Icon }) => (
          <NavLink
            key={name}
            end
            to={base + path}
            className={({ isActive }) => (isActive ? "active" : "")}
          >
            <Icon className="size-4" />
            {name}
            {name === "Resources" && (
              <span className="tab-count">{number(data.resourceCount)}</span>
            )}
          </NavLink>
        ))}
      </nav>
      {section === "updates" || section === "previews" ? (
        <UpdatePage
          key={section + version}
          stackID={data.id}
          section={section}
          version={version!}
        />
      ) : section === "resources" ? (
        <Resources resources={data.state.resources ?? []} />
      ) : section === "history" ? (
        <HistoryTable
          key={data.id}
          stackID={data.id}
          initial={data.history}
          base={base}
        />
      ) : (
        <Overview data={data} base={base} />
      )}
    </>
  );
}
function Overview({ data, base }: { data: StackDetail; base: string }) {
  const root = data.state.resources?.find(
    (r) => r.type === "pulumi:pulumi:Stack",
  );
  const last = data.history[0];
  return (
    <>
      <div className="stats-grid detail-stats">
        <div className="stat-card">
          <div className="stat-label">
            Resources
            <Box className="size-4" />
          </div>
          <div className="stat-value">{number(data.resourceCount)}</div>
          <div className="stat-note">Managed in this stack</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">
            Current version
            <GitCommitHorizontal className="size-4" />
          </div>
          <div className="stat-value">v{data.version}</div>
          <div className="stat-note">
            {last ? "Updated " + date(last.startTime) : "No updates yet"}
          </div>
        </div>
        <div className="stat-card">
          <div className="stat-label">
            Latest operation
            <Clock3 className="size-4" />
          </div>
          <div className="stat-value status-value">
            {last ? <Status value={last.result} /> : "—"}
          </div>
          <div className="stat-note">
            {last?.kind ?? "Waiting for the first update"}
          </div>
        </div>
      </div>
      {data.activeUpdate && (
        <div className="notice">
          <RefreshCw className="size-4 animate-spin" />
          An update is running.
          <Link to={base + "/previews/" + data.activeUpdate}>
            View live operation
            <ArrowUpRight className="size-3" />
          </Link>
        </div>
      )}
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h2>Stack outputs</h2>
            <p>Exported values from your infrastructure.</p>
          </div>
          <Badge variant="outline">
            <ShieldCheck className="size-3" />
            Secrets hidden
          </Badge>
        </div>
        {root?.outputs && Object.keys(root.outputs).length ? (
          <div className="outputs-table">
            {Object.entries(root.outputs).map(([key, value]) => (
              <div className="output-row" key={key}>
                <code>{key}</code>
                <JsonView value={value} />
              </div>
            ))}
          </div>
        ) : (
          <Empty
            title="No outputs yet"
            description="Export values from your Pulumi program to see them here."
          />
        )}
      </section>
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h2>Latest update</h2>
            <p>The most recent operation on this stack.</p>
          </div>
          <Button variant="ghost" size="sm" asChild>
            <Link to={base + "/history"}>
              View history
              <ArrowRight className="size-3.5" />
            </Link>
          </Button>
        </div>
        {last ? (
          <Link
            to={base + "/updates/" + last.version}
            className="latest-update"
          >
            <span className="update-check">
              <CheckCircle2 className="size-5" />
            </span>
            <div>
              <strong>{last.message || "No update message"}</strong>
              <span>
                Version {last.version} · {last.kind} · {date(last.startTime)}
              </span>
            </div>
            <Status value={last.result} />
            <ArrowUpRight className="size-4" />
          </Link>
        ) : (
          <Empty
            title="No update history"
            description="Run pulumi up to create the first version."
          />
        )}
      </section>
    </>
  );
}
function Resources({ resources }: { resources: Resource[] }) {
  const [search, setSearch] = useState("");
  const visible = resources.filter((r) =>
    (r.urn + " " + r.type + " " + (r.id ?? ""))
      .toLowerCase()
      .includes(search.toLowerCase()),
  );
  return (
    <section className="panel">
      <div className="panel-heading">
        <div>
          <h2>
            Resources <span className="count-text">{resources.length}</span>
          </h2>
          <p>Inspect resource properties and dependencies.</p>
        </div>
        <div className="search-input resource-search">
          <Search className="size-4" />
          <Input
            aria-label="Search resources"
            placeholder="Find a resource…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
      </div>
      {!visible.length ? (
        <Empty
          title="No resources found"
          description="Resources will appear here after your next update."
        />
      ) : (
        <div className="resource-list">
          {visible.map((r, i) => (
            <details key={r.urn + ":" + i} className="resource-item">
              <summary>
                <span className="resource-type-icon">
                  <Box className="size-4" />
                </span>
                <span className="resource-title">
                  <strong>{r.urn.split("::").at(-1)}</strong>
                  <code>{r.type}</code>
                </span>
                {r.viewOf && <Badge variant="secondary">View</Badge>}
                <ChevronDown className="size-4" />
              </summary>
              <div className="resource-properties">
                <div className="property-label">URN</div>
                <code className="urn">{r.urn}</code>
                {r.id && (
                  <>
                    <div className="property-label">ID</div>
                    <code className="urn">{r.id}</code>
                  </>
                )}
                {r.parent && (
                  <>
                    <div className="property-label">PARENT</div>
                    <code className="urn">{r.parent}</code>
                  </>
                )}
                <div className="property-label">INPUTS</div>
                <JsonView value={r.inputs} />
                <div className="property-label">OUTPUTS</div>
                <JsonView value={r.outputs} />
              </div>
            </details>
          ))}
        </div>
      )}
    </section>
  );
}
function HistoryTable({
  stackID,
  initial,
  base,
}: {
  stackID: string;
  initial: History[];
  base: string;
}) {
  const [page, setPage] = useState(1);
  const query = useQuery({
    queryKey: ["history", stackID, page],
    queryFn: ({ signal }) =>
      request<History[]>("/stacks/" + stackID + "/history?page=" + page, {
        signal,
      }).then((r) => r.data),
    initialData: page === 1 ? initial : undefined,
    retry: false,
  });
  return (
    <section className="panel">
      <div className="panel-heading">
        <div>
          <h2>Update history</h2>
          <p>Every operation, with its reason and result.</p>
        </div>
        <Badge variant="outline">Page {page}</Badge>
      </div>
      {query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <ErrorState error={query.error} retry={() => void query.refetch()} />
      ) : !query.data.length ? (
        <Empty
          title="No updates on this page"
          description="Run pulumi up to record an update."
        />
      ) : (
        <div className="table-scroll">
          <table className="history-table">
            <thead>
              <tr>
                <th>VERSION / MESSAGE</th>
                <th>OPERATION</th>
                <th>STATUS</th>
                <th>STARTED</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {query.data.map((row) => (
                <tr key={row.updateID}>
                  <td>
                    <Link to={base + "/updates/" + row.version}>
                      <span className="version-tag">v{row.version}</span>
                      <strong>{row.message || "No update message"}</strong>
                    </Link>
                  </td>
                  <td className="capitalize">{row.kind}</td>
                  <td>
                    <Status value={row.result} />
                  </td>
                  <td className="whitespace-nowrap">{date(row.startTime)}</td>
                  <td>
                    <Link
                      to={base + "/updates/" + row.version}
                      aria-label={"View version " + row.version}
                    >
                      <ArrowUpRight className="size-4" />
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="pagination">
        <Button
          variant="outline"
          size="sm"
          disabled={page === 1 || query.isFetching}
          onClick={() => setPage((p) => p - 1)}
        >
          Previous
        </Button>
        <span>Page {page}</span>
        <Button
          variant="outline"
          size="sm"
          disabled={!query.data || query.data.length < 100 || query.isFetching}
          onClick={() => setPage((p) => p + 1)}
        >
          Next
        </Button>
      </div>
    </section>
  );
}
function UpdatePage({
  stackID,
  section,
  version,
}: {
  stackID: string;
  section: string;
  version: string;
}) {
  const url =
    section === "updates"
      ? "/stacks/" + stackID + "/updates/" + encodeURIComponent(version)
      : "/updates/" + encodeURIComponent(version);
  const query = useQuery({
    queryKey: ["update", stackID, section, version],
    queryFn: ({ signal }) =>
      request<UpdateDetail>(url, { signal }).then((r) => r.data),
    retry: false,
    refetchInterval: (q) => (q.state.data?.status === "running" ? 3000 : false),
  });
  if (query.isPending) return <Loading label="Loading operation…" />;
  if (query.isError)
    return (
      <ErrorState error={query.error} retry={() => void query.refetch()} />
    );
  const d = query.data;
  return (
    <>
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h2>
              {d.dryRun ? "Preview" : "Update"}{" "}
              {d.version ? "· v" + d.version : ""}
            </h2>
            <p>{d.request.metadata?.message || "No update message"}</p>
          </div>
          <Status value={d.status} />
        </div>
        <div className="operation-info">
          <div>
            <span>Operation</span>
            <strong>{d.kind}</strong>
          </div>
          <div>
            <span>Started</span>
            <strong>{date(d.startedAt)}</strong>
          </div>
          <div>
            <span>Finished</span>
            <strong>{date(d.endedAt)}</strong>
          </div>
        </div>
        {d.diff && d.diff.length > 0 && (
          <div className="diff-list">
            {d.diff.map((change, i) => (
              <div key={change.urn + i}>
                <Badge variant="outline">{change.action}</Badge>
                <code>{change.urn.split("::").at(-1)}</code>
                <span>{change.paths?.join(", ")}</span>
              </div>
            ))}
          </div>
        )}
      </section>
      <EventLog key={d.id} id={d.id} />
    </>
  );
}
type EngineEvent = {
  sequence: number;
  timestamp: number;
  stdoutEvent?: { message?: string };
  diagnosticEvent?: { message?: string; severity?: string };
  summaryEvent?: { message?: string };
  [key: string]: unknown;
};
function EventLog({ id }: { id: string }) {
  const [events, setEvents] = useState<Map<number, EngineEvent>>(new Map());
  const [error, setError] = useState("");
  const [closed, setClosed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const cursor = useRef("");
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      try {
        const res = await request<{
          events: EngineEvent[];
          continuationToken?: string | null;
        }>(
          "/updates/" +
            id +
            "/events" +
            (cursor.current
              ? "?cursor=" + encodeURIComponent(cursor.current)
              : ""),
          { signal: controller.signal },
        );
        if (controller.signal.aborted) return;
        setError("");
        setEvents((old) => {
          const next = new Map(old);
          for (const e of res.data.events) next.set(e.sequence, e);
          return next;
        });
        cursor.current = res.data.continuationToken ?? "";
        setClosed(!cursor.current);
        if (cursor.current)
          timer = setTimeout(poll, res.data.events.length >= 100 ? 20 : 2000);
      } catch (e) {
        if (!controller.signal.aborted) {
          setError(e instanceof Error ? e.message : "Unable to load logs");
          timer = setTimeout(poll, 5000);
        }
      }
    }
    void poll();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [id, attempt]);
  const sorted = [...events.values()].sort((a, b) => a.sequence - b.sequence);
  return (
    <section className="panel log-panel">
      <div className="panel-heading">
        <div>
          <h2>
            <Terminal className="size-4" />
            Operation logs
          </h2>
          <p>
            {error
              ? "Connection interrupted"
              : closed
                ? "Log collection finished"
                : "Logs are still being collected"}
          </p>
        </div>
        <Badge variant="outline">{closed ? "Complete" : "Live"}</Badge>
      </div>
      {error && (
        <div className="log-error" role="alert">
          {error}
          <Button
            size="sm"
            variant="outline"
            onClick={() => setAttempt((a) => a + 1)}
          >
            Retry
          </Button>
        </div>
      )}
      <div id="events" className="event-log" aria-live="polite">
        {sorted.length ? (
          sorted.map((e) => (
            <div className="event-line" key={e.sequence}>
              <span className="event-sequence">
                {String(e.sequence).padStart(3, "0")}
              </span>
              <pre>
                {e.stdoutEvent?.message ??
                  e.diagnosticEvent?.message ??
                  (e.summaryEvent
                    ? "Update summary\n" +
                      JSON.stringify(e.summaryEvent, null, 2)
                    : JSON.stringify(e, null, 2))}
              </pre>
            </div>
          ))
        ) : (
          <span className="log-placeholder">
            {closed
              ? "No events were recorded for this operation."
              : "Waiting for events…"}
          </span>
        )}
      </div>
    </section>
  );
}
