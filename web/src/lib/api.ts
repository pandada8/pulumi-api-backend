export type Stack = {
  id: string;
  orgName: string;
  projectName: string;
  stackName: string;
  resourceCount: number;
};
export type Resource = {
  urn: string;
  type: string;
  id?: string;
  parent?: string;
  inputs?: Record<string, unknown>;
  outputs?: Record<string, unknown>;
  viewOf?: string;
};
export type History = {
  updateID: string;
  version: number;
  kind: string;
  result: string;
  message: string;
  startTime: number;
  endTime: number;
  environment?: Record<string, string>;
};
export type StackDetail = {
  id: string;
  org: string;
  project: string;
  name: string;
  status: string;
  version: number;
  resourceCount: number;
  headGeneration: number;
  activeUpdate: string;
  state: { resources?: Resource[] };
  history: History[];
};
export type UpdateDetail = {
  id: string;
  version: number;
  kind: string;
  status: string;
  dryRun: boolean;
  startedAt: string;
  endedAt: string;
  eventsClosed: boolean;
  request: {
    metadata?: { message?: string; environment?: Record<string, string> };
  };
  diff?: { urn: string; action: string; paths?: string[] }[];
};
export type Envelope<T> = { data: T; nextCursor?: string };
export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
export async function request<T>(
  path: string,
  options: RequestInit = {},
): Promise<Envelope<T>> {
  const res = await fetch("/console/api" + path, {
    credentials: "same-origin",
    ...options,
    headers: {
      ...(options.body ? { "Content-Type": "application/json" } : {}),
      ...options.headers,
    },
  });
  const data = await res.json();
  if (!res.ok) {
    if (res.status === 401)
      window.dispatchEvent(new Event("pulumid:unauthorized"));
    throw new ApiError(res.status, data.message || "Request failed");
  }
  return data;
}
export const stackPath = (s: Stack) =>
  "/" +
  [s.orgName, s.projectName, s.stackName].map(encodeURIComponent).join("/");
export const number = (n: number) => new Intl.NumberFormat("en-US").format(n);
export function date(value: number | string) {
  if (!value || value === "0001-01-01T00:00:00Z") return "—";
  const d = new Date(typeof value === "number" ? value * 1000 : value);
  if (!Number.isFinite(d.getTime()) || d.getFullYear() < 1970) return "—";
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}
