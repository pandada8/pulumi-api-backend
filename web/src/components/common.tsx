import {
  AlertCircle,
  ArrowRight,
  Check,
  CheckCircle2,
  Circle,
  Copy,
  Loader2,
  XCircle,
} from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "./ui/button";
import { Badge } from "./ui/badge";
import { Skeleton } from "./ui/skeleton";
import { ApiError } from "@/lib/api";
import { cn } from "@/lib/utils";

export function Logo({ small = false }: { small?: boolean }) {
  return (
    <span className={cn("brand-mark", small && "small")} aria-hidden="true">
      <span />
      <span />
      <span />
    </span>
  );
}
export function Status({ value }: { value: string }) {
  const running = ["running", "in-progress", "created", "not-started"].includes(
    value,
  );
  const failed = ["failed", "cancelled", "invalid"].includes(value);
  const Icon = running
    ? Loader2
    : failed
      ? XCircle
      : value === "succeeded"
        ? CheckCircle2
        : Circle;
  return (
    <Badge
      variant="outline"
      className={cn(
        "status-badge",
        running ? "running" : failed ? "failed" : "healthy",
      )}
    >
      <Icon className={cn("size-3", running && "animate-spin")} />
      {value === "idle" ? "Ready" : value.replaceAll("-", " ")}
    </Badge>
  );
}
export function Loading({
  label = "Loading your workspace…",
}: {
  label?: string;
}) {
  return (
    <div className="loading-state" role="status">
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" />
        {label}
      </div>
      {[1, 2, 3].map((i) => (
        <Skeleton key={i} className="h-12 w-full" />
      ))}
    </div>
  );
}
export function ErrorState({
  error,
  retry,
}: {
  error: Error | null;
  retry?: () => void;
}) {
  return (
    <div className="empty-state" role="alert">
      <AlertCircle className="size-7 text-muted-foreground" />
      <h2>
        {error instanceof ApiError && error.status === 404
          ? "This page could not be found"
          : "We couldn’t load this view"}
      </h2>
      <p>{error?.message || "Please try again."}</p>
      <div className="flex gap-2">
        {retry && (
          <Button variant="outline" onClick={retry}>
            Try again
          </Button>
        )}
        <Button variant="ghost" asChild>
          <Link to="/stacks">
            Back to stacks <ArrowRight className="size-4" />
          </Link>
        </Button>
      </div>
    </div>
  );
}
export function Empty({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children?: React.ReactNode;
}) {
  return (
    <div className="empty-state">
      <Circle className="size-7 text-muted-foreground" />
      <h2>{title}</h2>
      <p>{description}</p>
      {children}
    </div>
  );
}
export function JsonView({ value }: { value: unknown }) {
  const [copied, setCopied] = useState(false);
  const text = JSON.stringify(value ?? {}, null, 2);
  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      setCopied(false);
    }
  }
  return (
    <div className="json-view">
      <Button
        size="icon"
        variant="ghost"
        className="copy-button"
        onClick={copy}
        aria-label={copied ? "Copied" : "Copy JSON"}
      >
        {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
      </Button>
      <pre>{text}</pre>
    </div>
  );
}
