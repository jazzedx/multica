import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { RuntimeDirectoryListing } from "../types";

// Choosing a folder on a runtime's machine from the web: the page asks the
// machine which directories exist under what has been typed so far, one level
// at a time, and narrows them to the typed prefix itself.

export const runtimeDirectoryKeys = {
  all: () => ["runtimes", "directories"] as const,
  listing: (runtimeId: string, path: string) =>
    [...runtimeDirectoryKeys.all(), runtimeId, path] as const,
};

/**
 * `unavailable` means the machine gave no answer: it is offline, its daemon
 * predates directory listings, or the caller may not use it. The path can
 * still be typed and saved; it just is not checked first.
 */
export type RuntimeDirectoryResult =
  | { state: "listed"; listing: RuntimeDirectoryListing }
  | { state: "unavailable" };

// The server answers within a few seconds or gives up, so a handful of reads
// is all a request can need.
const MAX_READS = 6;
const READ_GAP_MS = 150;

export async function readRuntimeDirectory(
  runtimeId: string,
  path: string,
  options: { probeWrite?: boolean; signal?: AbortSignal } = {},
): Promise<RuntimeDirectoryResult> {
  try {
    let req = await api.listRuntimeDirectory(runtimeId, path, options);
    for (
      let read = 0;
      read < MAX_READS && (req.status === "pending" || req.status === "running");
      read++
    ) {
      await new Promise((resolve) => setTimeout(resolve, READ_GAP_MS));
      req = await api.getRuntimeDirectoryRequest(runtimeId, req.id, options.signal);
    }
    if (req.status === "completed" && req.listing) {
      return { state: "listed", listing: req.listing };
    }
    return { state: "unavailable" };
  } catch (err) {
    if (options.signal?.aborted) throw err;
    return { state: "unavailable" };
  }
}

export function runtimeDirectoryOptions(runtimeId: string, path: string) {
  return queryOptions({
    queryKey: runtimeDirectoryKeys.listing(runtimeId, path),
    queryFn: ({ signal }) => readRuntimeDirectory(runtimeId, path, { signal }),
    staleTime: 15_000,
    gcTime: 5 * 60_000,
    retry: false,
  });
}

const SEPARATORS = /[\\/]/;

/**
 * Splits what has been typed into the directory to list and the prefix to
 * narrow its entries by: "/srv/ap" lists "/srv/" for names starting "ap".
 */
export function splitDirectoryInput(text: string): { parent: string; leaf: string } {
  let cut = -1;
  for (let i = text.length - 1; i >= 0; i--) {
    if (SEPARATORS.test(text[i]!)) {
      cut = i;
      break;
    }
  }
  return { parent: text.slice(0, cut + 1), leaf: text.slice(cut + 1) };
}

/**
 * The path to ask the machine about, without the trailing separator a
 * half-typed path ends in. A root keeps its separator: "/" and "C:\" are
 * directories, while "" asks for the home directory and "C:" is not absolute.
 */
export function directoryPath(text: string): string {
  const trimmed = text.trim();
  if (trimmed === "") return "";
  const stripped = trimmed.replace(/[\\/]+$/, "");
  if (stripped === "") return trimmed[0]!;
  if (/^[A-Za-z]:$/.test(stripped)) return `${stripped}\\`;
  return stripped;
}

/** Whether a typed path is absolute on either kind of machine. */
export function isAbsoluteDirectoryPath(text: string): boolean {
  return /^(\/|[A-Za-z]:[\\/]|\\\\)/.test(text.trim());
}

export function joinDirectoryPath(listing: RuntimeDirectoryListing, name: string): string {
  const base = listing.path;
  return SEPARATORS.test(base[base.length - 1] ?? "")
    ? `${base}${name}`
    : `${base}${listing.separator}${name}`;
}

export function directoryBaseName(path: string): string {
  const parts = directoryPath(path).split(SEPARATORS).filter(Boolean);
  return parts[parts.length - 1] ?? path;
}

/**
 * The entries a typed prefix could be completed to. Names differing only in
 * case still match, with exact-case matches first, and dot directories stay
 * out of the way until a dot is typed.
 */
export function completeDirectoryNames(entries: string[], leaf: string): string[] {
  const lower = leaf.toLowerCase();
  const exact: string[] = [];
  const loose: string[] = [];
  for (const name of entries) {
    if (name.startsWith(".") && !leaf.startsWith(".")) continue;
    if (name.startsWith(leaf)) exact.push(name);
    else if (name.toLowerCase().startsWith(lower)) loose.push(name);
  }
  return [...exact, ...loose];
}

/**
 * Whether a listing holds an entry of this name. Windows names are compared
 * without case, as the machine itself would.
 */
export function directoryHasEntry(listing: RuntimeDirectoryListing, name: string): boolean {
  if (listing.separator === "\\") {
    const lower = name.toLowerCase();
    return listing.entries.some((entry) => entry.toLowerCase() === lower);
  }
  return listing.entries.includes(name);
}
