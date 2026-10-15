import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import { Link, useSearchParams } from "react-router-dom";
import {
  ArrowDownUp,
  ArrowUpRight,
  Box,
  Building2,
  ChevronDown,
  ChevronRight,
  ChevronsDownUp,
  ChevronsUpDown,
  Folder,
  FolderOpen,
  Layers3,
  RefreshCw,
  Search,
  X,
} from "lucide-react";
import { Button } from "./ui/button";
import { Badge } from "./ui/badge";
import { Input } from "./ui/input";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "./ui/collapsible";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { Empty, ErrorState, Loading } from "./common";
import { number, stackPath, type Stack } from "@/lib/api";
import { useStacks } from "@/lib/queries";

type Project = { name: string; stacks: Stack[] };
function treeKeys(stacks: Stack[]) {
  return [
    ...new Set(
      stacks.flatMap((s) => [
        "org:" + s.orgName,
        "project:" + s.orgName + "/" + s.projectName,
      ]),
    ),
  ];
}
function treeKeyboard(e: KeyboardEvent<HTMLDivElement>) {
  const nodes = [
    ...e.currentTarget.querySelectorAll<HTMLElement>("[data-tree-node]"),
  ].filter((n) => n.getClientRects().length);
  const focused = document.activeElement as HTMLElement;
  const index = nodes.indexOf(focused);
  if (index < 0) return;
  let next: HTMLElement | undefined;
  if (e.key === "ArrowDown") next = nodes[index + 1];
  if (e.key === "ArrowUp") next = nodes[index - 1];
  if (e.key === "Home") next = nodes[0];
  if (e.key === "End") next = nodes.at(-1);
  if (
    (e.key === "ArrowRight" &&
      focused.getAttribute("aria-expanded") === "false") ||
    (e.key === "ArrowLeft" && focused.getAttribute("aria-expanded") === "true")
  ) {
    e.preventDefault();
    focused.click();
    return;
  }
  if (next) {
    e.preventDefault();
    next.focus();
  }
}
export function StackTree() {
  const query = useStacks();
  const [params, setParams] = useSearchParams();
  const search = params.get("q") ?? "";
  const org = params.get("org") ?? "";
  const projectFilter = params.get("project") ?? "";
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [descending, setDescending] = useState(false);
  const searchRef = useRef<HTMLInputElement>(null);
  const stacks = query.stacks;
  const projects = new Set(stacks.map((s) => s.orgName + "/" + s.projectName))
    .size;
  const organizations = [...new Set(stacks.map((s) => s.orgName))].sort();
  const resources = stacks.reduce((n, s) => n + s.resourceCount, 0);
  const filtered = useMemo(
    () =>
      stacks.filter(
        (s) =>
          (!org || s.orgName === org) &&
          (!projectFilter || s.projectName === projectFilter) &&
          [s.orgName, s.projectName, s.stackName]
            .join("/")
            .toLowerCase()
            .includes(search.toLowerCase().trim()),
      ),
    [stacks, org, projectFilter, search],
  );
  const groups = useMemo(() => {
    const map = new Map<string, Map<string, Stack[]>>();
    for (const s of filtered) {
      if (!map.has(s.orgName)) map.set(s.orgName, new Map());
      const p = map.get(s.orgName)!;
      if (!p.has(s.projectName)) p.set(s.projectName, []);
      p.get(s.projectName)!.push(s);
    }
    const compare = (a: string, b: string) =>
      a.localeCompare(b, undefined, { numeric: true }) * (descending ? -1 : 1);
    return [...map]
      .sort(([a], [b]) => compare(a, b))
      .map(([name, projects]) => ({
        name,
        projects: [...projects]
          .sort(([a], [b]) => compare(a, b))
          .map(([name, stacks]): Project => ({
            name,
            stacks: stacks.sort((a, b) => compare(a.stackName, b.stackName)),
          })),
      }));
  }, [filtered, descending]);
  function setFilter(key: string, value: string) {
    setParams(
      (p) => {
        if (value) p.set(key, value);
        else p.delete(key);
        return p;
      },
      { replace: true },
    );
  }
  function toggle(key: string, open: boolean) {
    setCollapsed((old) => {
      const next = new Set(old);
      if (open) next.delete(key);
      else next.add(key);
      return next;
    });
  }
  useEffect(() => {
    setCollapsed(new Set());
  }, [search, org, projectFilter]);
  return (
    <>
      <div className="page-heading">
        <div>
          <div className="eyebrow">YOUR INFRASTRUCTURE, ORGANIZED</div>
          <h1>
            Stacks<span className="title-dot">.</span>
          </h1>
          <p>
            A home for every environment. Explore your stacks by organization
            and project.
          </p>
        </div>
        <Button
          variant="outline"
          onClick={() => void query.refetch()}
          disabled={query.isFetching}
          aria-label="Refresh stacks"
        >
          <RefreshCw
            className={query.isFetching ? "size-4 animate-spin" : "size-4"}
          />
          Refresh
        </Button>
      </div>
      <div className="stats-grid">
        {[
          {
            label: "Total stacks",
            value: stacks.length,
            icon: Layers3,
            note: `Across ${number(organizations.length)} organization${organizations.length === 1 ? "" : "s"}`,
          },
          {
            label: "Projects",
            value: projects,
            icon: Folder,
            note: "Grouped by workspace",
          },
          {
            label: "Managed resources",
            value: resources,
            icon: Box,
            note: "Across all visible stacks",
          },
        ].map(({ label, value, icon: Icon, note }) => (
          <div className="stat-card" key={label}>
            <div className="stat-label">
              {label}
              <Icon className="size-4" />
            </div>
            <div className="stat-value">
              {query.isPending ? "—" : number(value)}
              {query.hasNextPage && <span className="stat-plus">+</span>}
            </div>
            <div className="stat-note">{note}</div>
          </div>
        ))}
      </div>
      <section className="tree-card" aria-label="Stack explorer">
        <div className="tree-toolbar">
          <div className="tree-card-title">
            <Layers3 className="size-4" />
            <h2>Stack explorer</h2>
            <Badge variant="secondary">{number(filtered.length)}</Badge>
          </div>
          <div className="tree-tools">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setCollapsed(new Set())}
            >
              <ChevronsUpDown className="size-3.5" />
              <span className="desktop-label">Expand all</span>
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setCollapsed(new Set(treeKeys(stacks)))}
            >
              <ChevronsDownUp className="size-3.5" />
              <span className="desktop-label">Collapse all</span>
            </Button>
          </div>
        </div>
        <div className="filter-bar">
          <div className="search-input">
            <Search className="size-4" />
            <Input
              ref={searchRef}
              aria-label="Search stacks"
              placeholder="Search stacks, projects, organizations…"
              value={search}
              onChange={(e) => setFilter("q", e.target.value)}
            />
            {search ? (
              <button
                className="search-clear"
                aria-label="Clear search"
                onClick={() => setFilter("q", "")}
              >
                <X className="size-3.5" />
              </button>
            ) : (
              <kbd>/</kbd>
            )}
          </div>
          <DropdownMenu modal={false}>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" className="org-filter">
                <Building2 className="size-3.5" />
                <span>{org || "All organizations"}</span>
                <ChevronDown className="size-3.5" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => setFilter("org", "")}>
                All organizations
              </DropdownMenuItem>
              {organizations.map((o) => (
                <DropdownMenuItem key={o} onClick={() => setFilter("org", o)}>
                  {o}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
        {projectFilter && (
          <div className="filter-chip">
            Project: {projectFilter}
            <button
              onClick={() => setFilter("project", "")}
              aria-label="Clear project filter"
            >
              <X className="size-3" />
            </button>
          </div>
        )}
        {query.isPending ? (
          <Loading label="Loading stacks…" />
        ) : query.isError ? (
          <ErrorState
            error={query.error}
            retry={() =>
              void (query.hasNextPage ? query.fetchNextPage() : query.refetch())
            }
          />
        ) : !stacks.length ? (
          <Empty
            title="Your workspace starts here"
            description="Create a stack with the Pulumi CLI and it will appear here."
          >
            <code className="empty-command">pulumi stack init dev</code>
          </Empty>
        ) : !filtered.length ? (
          <Empty
            title="No matching stacks"
            description="Try another name or clear your filters."
          >
            <Button variant="outline" onClick={() => setParams({})}>
              Clear filters
            </Button>
          </Empty>
        ) : (
          <>
            <div className="tree-columns">
              <button onClick={() => setDescending(!descending)}>
                NAME <ArrowDownUp className="size-3" />
              </button>
              <span>RESOURCES</span>
              <span />
            </div>
            <div
              className="stack-tree"
              onKeyDown={treeKeyboard}
              aria-label="Stack tree"
            >
              {groups.map((group) => {
                const orgKey = "org:" + group.name,
                  orgOpen = !collapsed.has(orgKey);
                const total = group.projects.reduce(
                  (n, p) => n + p.stacks.length,
                  0,
                );
                return (
                  <Collapsible
                    key={group.name}
                    open={orgOpen}
                    onOpenChange={(open) => toggle(orgKey, open)}
                  >
                    <CollapsibleTrigger className="org-row" data-tree-node>
                      <ChevronRight
                        className={"tree-chevron " + (orgOpen ? "open" : "")}
                      />
                      <span className="org-avatar">
                        {group.name.slice(0, 1).toUpperCase()}
                      </span>
                      <span className="tree-name">{group.name}</span>
                      <span className="group-kind">Organization</span>
                      <Badge variant="secondary">{number(total)} stacks</Badge>
                    </CollapsibleTrigger>
                    <CollapsibleContent>
                      <div className="org-children">
                        {group.projects.map((project) => {
                          const key =
                              "project:" + group.name + "/" + project.name,
                            open = !collapsed.has(key);
                          return (
                            <Collapsible
                              key={key}
                              open={open}
                              onOpenChange={(value) => toggle(key, value)}
                            >
                              <CollapsibleTrigger
                                className="project-row"
                                data-tree-node
                              >
                                <ChevronRight
                                  className={
                                    "tree-chevron " + (open ? "open" : "")
                                  }
                                />
                                {open ? (
                                  <FolderOpen className="size-4 folder-icon" />
                                ) : (
                                  <Folder className="size-4 folder-icon" />
                                )}
                                <span className="tree-name">
                                  {project.name}
                                </span>
                                <span className="project-count">
                                  {project.stacks.length}
                                </span>
                                <span className="row-meta">Project</span>
                              </CollapsibleTrigger>
                              <CollapsibleContent>
                                <div className="project-children">
                                  {project.stacks.map((stack) => (
                                    <Link
                                      key={stack.id}
                                      to={stackPath(stack)}
                                      className="stack-row"
                                      data-tree-node
                                    >
                                      <span className="branch-line" />
                                      <span className="stack-icon">
                                        <Layers3 className="size-3.5" />
                                      </span>
                                      <span className="tree-name">
                                        {stack.stackName}
                                      </span>
                                      <span className="stack-resource-count">
                                        {number(stack.resourceCount)}
                                        <span className="sr-only">
                                          {" "}
                                          resources
                                        </span>
                                      </span>
                                      <ArrowUpRight className="size-3.5 row-arrow" />
                                    </Link>
                                  ))}
                                </div>
                              </CollapsibleContent>
                            </Collapsible>
                          );
                        })}
                      </div>
                    </CollapsibleContent>
                  </Collapsible>
                );
              })}
            </div>
          </>
        )}
        <div className="tree-footer">
          <span>
            <span className="live-dot" />
            {query.hasNextPage
              ? `Loading more stacks · ${number(stacks.length)} loaded`
              : `${number(filtered.length)} stack${filtered.length === 1 ? "" : "s"}${filtered.length !== stacks.length ? ` of ${number(stacks.length)}` : ""} in your workspace`}
          </span>
          <span className="desktop-label">
            <kbd>↑</kbd>
            <kbd>↓</kbd> to navigate <span className="footer-separator">·</span>{" "}
            <kbd>↵</kbd> to open
          </span>
        </div>
      </section>
      <div className="workspace-note">
        <span className="tiny-mark">◇</span> One workspace. Every environment.{" "}
        <span>Powered by pulumid</span>
      </div>
    </>
  );
}
