"use client";

import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Check, FolderOpen, Plus, TriangleAlert } from "@multica/ui/icons";
import { toast } from "sonner";
import { useCreateProjectResource } from "@multica/core/projects";
import {
  runtimeAdvertisesLocalWorktree,
  runtimeListOptions,
} from "@multica/core/runtimes";
import {
  completeDirectoryNames,
  directoryBaseName,
  directoryHasEntry,
  directoryPath,
  isAbsoluteDirectoryPath,
  joinDirectoryPath,
  readRuntimeDirectory,
  runtimeDirectoryOptions,
  splitDirectoryInput,
  type RuntimeDirectoryResult,
} from "@multica/core/runtimes/directories";
import { useConfigStore } from "@multica/core/config";
import type {
  AgentRuntime,
  LocalDirectoryExecutionMode,
  LocalDirectoryResourceRef,
  ProjectResource,
  RuntimeDirectoryListing,
  RuntimeDirectoryProblem,
} from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Spinner } from "@multica/ui/components/ui/spinner";
import { cn } from "@multica/ui/lib/utils";
import { useDebouncedValue } from "../../common/use-debounced-value";
import { buildRuntimeMachines } from "../../runtimes/components/runtime-machines";
import { useT } from "../../i18n";
import {
  LocalDirectoryModeOptions,
  type WorktreeUnavailableReason,
} from "./local-directory-mode-options";

// Adding a local folder for a computer other than the one the page is open
// on: a server reached over SSH has no desktop to pick a folder from. The
// path is typed, and the computer's own daemon completes and checks it.

type LocalDirectoryResource = ProjectResource & {
  resource_ref: LocalDirectoryResourceRef;
};

export interface DirectoryMachine {
  daemonId: string;
  title: string;
  online: boolean;
  /** An online runtime of the computer to ask through; null when it has none. */
  runtimeId: string | null;
  /** The project already has a folder on this computer, and it takes one. */
  taken: boolean;
}

export function directoryMachines(
  runtimes: AgentRuntime[],
  folders: LocalDirectoryResource[],
  now: number,
): DirectoryMachine[] {
  const taken = new Set(folders.map((f) => f.resource_ref.daemon_id));
  const machines: DirectoryMachine[] = [];
  for (const machine of buildRuntimeMachines(runtimes, { now })) {
    if (!machine.daemonId || machine.section === "cloud") continue;
    machines.push({
      daemonId: machine.daemonId,
      title: machine.title,
      online: machine.health === "online",
      runtimeId: machine.runtimes.find((r) => r.status === "online")?.id ?? null,
      taken: taken.has(machine.daemonId),
    });
  }
  return machines;
}

/**
 * What is known about the typed path. `unknown` is a path the computer could
 * not be asked about; it can still be saved, and the daemon checks it when a
 * task starts there.
 */
export type DirectoryStatus =
  | { kind: "empty" }
  | { kind: "reading" }
  | { kind: "unknown" }
  | { kind: "problem"; problem: RuntimeDirectoryProblem }
  | { kind: "ok"; listing: RuntimeDirectoryListing };

function statusOf(listing: RuntimeDirectoryListing): DirectoryStatus {
  return listing.problem
    ? { kind: "problem", problem: listing.problem }
    : { kind: "ok", listing };
}

export function directoryStatus(input: {
  text: string;
  canAsk: boolean;
  /** The listing of the directory the typed path sits in. */
  parent: RuntimeDirectoryResult | undefined;
  /** The listing of the typed path itself, asked for once its name is known to exist. */
  target: RuntimeDirectoryResult | undefined;
}): DirectoryStatus {
  const { text, canAsk, parent, target } = input;
  if (text.trim() === "") return { kind: "empty" };
  if (!isAbsoluteDirectoryPath(text)) return { kind: "problem", problem: "not_absolute" };
  if (!canAsk) return { kind: "unknown" };
  if (!parent) return { kind: "reading" };
  if (parent.state === "unavailable") return { kind: "unknown" };

  const { leaf } = splitDirectoryInput(text);
  if (leaf === "") return statusOf(parent.listing);
  // A directory that cannot be listed holds nothing that can be chosen.
  if (parent.listing.problem && parent.listing.problem !== "protected") {
    return {
      kind: "problem",
      problem: parent.listing.problem === "not_readable" ? "not_readable" : "not_found",
    };
  }
  if (!directoryHasEntry(parent.listing, leaf)) {
    return parent.listing.truncated ? { kind: "unknown" } : { kind: "problem", problem: "not_found" };
  }
  if (!target) return { kind: "reading" };
  return target.state === "unavailable" ? { kind: "unknown" } : statusOf(target.listing);
}

export function RemoteDirectoryAdder({
  wsId,
  projectId,
  folders,
  standalone,
}: {
  wsId: string;
  projectId: string;
  folders: LocalDirectoryResource[];
  /** Nothing else introduces local folders here, so this does. */
  standalone: boolean;
}) {
  const { t } = useT("projects");
  const { data: runtimes = [] } = useQuery(runtimeListOptions(wsId));
  const [open, setOpen] = useState(false);
  const machines = useMemo(
    () => directoryMachines(runtimes, folders, Date.now()),
    [runtimes, folders],
  );
  if (!machines.some((m) => !m.taken)) return null;

  return (
    // Under the folders already listed, this belongs to their group.
    <div className={cn("space-y-1.5", !standalone && "-mt-2")}>
      {standalone && (
        <>
          <div className="flex h-6 items-center text-micro font-medium text-muted-foreground">
            {t(($) => $.resources.local_group)}
          </div>
          <p className="-mt-1 text-micro text-muted-foreground">
            {t(($) => $.resources.local_group_hint)}
          </p>
        </>
      )}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogTrigger
          render={<Button variant="outline" size="sm" className="h-7 text-caption" />}
        >
          <Plus className="size-3" />
          {t(($) => $.resources.remote_add)}
        </DialogTrigger>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t(($) => $.resources.remote_title)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.resources.remote_description)}
            </DialogDescription>
          </DialogHeader>
          <RemoteDirectoryForm
            wsId={wsId}
            projectId={projectId}
            machines={machines}
            runtimes={runtimes}
            onDone={() => setOpen(false)}
          />
        </DialogContent>
      </Dialog>
    </div>
  );
}

function RemoteDirectoryForm({
  wsId,
  projectId,
  machines,
  runtimes,
  onDone,
}: {
  wsId: string;
  projectId: string;
  machines: DirectoryMachine[];
  runtimes: AgentRuntime[];
  onDone: () => void;
}) {
  const { t } = useT("projects");
  const createResource = useCreateProjectResource(wsId, projectId);
  const serverValidatesWorktree = useConfigStore((s) => s.localWorktreeSupported);
  const machineFieldId = useId();
  const pathFieldId = useId();
  const listId = useId();
  const inputRef = useRef<HTMLInputElement>(null);

  const [daemonId, setDaemonId] = useState(
    () => (machines.find((m) => !m.taken && m.online) ?? machines.find((m) => !m.taken))?.daemonId ?? "",
  );
  const [text, setText] = useState("");
  const [chosenMode, setChosenMode] = useState<LocalDirectoryExecutionMode | null>(null);
  // -1 until the arrow keys pick a suggestion; Tab takes the first one then.
  const [active, setActive] = useState(-1);
  const [listHidden, setListHidden] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Computers that gave no answer. One that does not answer once is not asked
  // again while the dialog is open: each further question would hold the form
  // for as long as the server waits before giving up.
  const [silent, setSilent] = useState<ReadonlySet<string>>(() => new Set());

  const machine = machines.find((m) => m.daemonId === daemonId);
  const runtimeId = machine?.runtimeId ?? null;
  const absolute = isAbsoluteDirectoryPath(text);
  const canAsk =
    runtimeId !== null && !silent.has(runtimeId) && (text.trim() === "" || absolute);

  // Typing within one level only narrows names already fetched, so the
  // directory being listed changes when a separator is typed and not before.
  const { parent, leaf } = splitDirectoryInput(text);
  const parentQuery = useQuery({
    ...runtimeDirectoryOptions(runtimeId ?? "", directoryPath(parent)),
    enabled: canAsk,
  });
  const parentListing =
    parentQuery.data?.state === "listed" ? parentQuery.data.listing : null;
  const unanswered = canAsk && parentQuery.data?.state === "unavailable";
  useEffect(() => {
    if (unanswered && runtimeId) setSilent((prev) => new Set(prev).add(runtimeId));
  }, [unanswered, runtimeId]);

  const targetPath = directoryPath(text);
  const settledTarget = useDebouncedValue(targetPath, 200);
  const leafExists =
    leaf !== "" && parentListing !== null && directoryHasEntry(parentListing, leaf);
  const targetQuery = useQuery({
    ...runtimeDirectoryOptions(runtimeId ?? "", targetPath),
    enabled: canAsk && leafExists && settledTarget === targetPath,
  });

  const status = directoryStatus({
    text,
    canAsk,
    parent: canAsk ? parentQuery.data : undefined,
    target: leafExists ? targetQuery.data : undefined,
  });

  const suggestions = useMemo(() => {
    if (!parentListing) return [];
    if (parentListing.problem && parentListing.problem !== "protected") return [];
    // Nothing typed yet: the home directory is where to start from.
    if (text.trim() === "") {
      return [{ label: parentListing.path, path: parentListing.path }];
    }
    return completeDirectoryNames(parentListing.entries, leaf).map((name) => ({
      label: name,
      path: joinDirectoryPath(parentListing, name),
    }));
  }, [parentListing, text, leaf]);
  const listShown = !listHidden && suggestions.length > 0;

  const isGitRepo = status.kind === "ok" ? status.listing.is_git_repo : undefined;
  const unavailableReason: WorktreeUnavailableReason | undefined =
    isGitRepo === false ? "not_git" : !serverValidatesWorktree ? "server_outdated" : undefined;
  const mode: LocalDirectoryExecutionMode =
    unavailableReason !== undefined
      ? "in_place"
      : (chosenMode ??
        (isGitRepo === true && runtimeAdvertisesLocalWorktree(runtimes, daemonId)
          ? "worktree"
          : "in_place"));

  const problemMessage = (problem: RuntimeDirectoryProblem): string => {
    switch (problem) {
      case "not_absolute":
        return t(($) => $.resources.remote_not_absolute);
      case "not_found":
        return t(($) => $.resources.local_validate_not_found);
      case "not_a_directory":
        return t(($) => $.resources.local_validate_not_a_directory);
      case "not_writable":
        return t(($) => $.resources.local_validate_not_writable);
      case "protected":
        return t(($) => $.resources.remote_protected);
      default:
        return t(($) => $.resources.local_validate_not_readable);
    }
  };

  const edit = (next: string) => {
    setText(next);
    setActive(-1);
    setListHidden(false);
    setError(null);
  };

  const accept = (path: string) => {
    // Ends on a separator, so what the folder holds is offered next.
    edit(`${path}${parentListing?.separator ?? "/"}`.replace(/([\\/])[\\/]+$/, "$1"));
    inputRef.current?.focus();
  };

  const canAdd =
    !saving &&
    machine !== undefined &&
    !machine.taken &&
    (status.kind === "ok" || status.kind === "unknown");

  const add = async () => {
    if (!canAdd || !machine) return;
    setSaving(true);
    setError(null);
    try {
      let path = targetPath;
      if (runtimeId && status.kind === "ok") {
        // Writing is only tried now: the check leaves a file in the folder for
        // a moment, which is not something to do to every folder typed past.
        const checked = await readRuntimeDirectory(runtimeId, targetPath, { probeWrite: true });
        if (checked.state === "listed") {
          if (checked.listing.problem) {
            setError(problemMessage(checked.listing.problem));
            return;
          }
          path = checked.listing.path;
        }
      }
      await createResource.mutateAsync({
        resource_type: "local_directory",
        resource_ref: {
          local_path: path,
          daemon_id: machine.daemonId,
          label: directoryBaseName(path),
          execution_mode: mode,
        },
      });
      toast.success(t(($) => $.resources.toast_local_attached));
      onDone();
    } catch (err) {
      setError(
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.resources.toast_attach_failed),
      );
    } finally {
      setSaving(false);
    }
  };

  const onKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.nativeEvent.isComposing) return;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      if (suggestions.length === 0) return;
      event.preventDefault();
      setListHidden(false);
      const step = event.key === "ArrowDown" ? 1 : -1;
      setActive((current) => {
        if (current === -1) return step === 1 ? 0 : suggestions.length - 1;
        return (current + step + suggestions.length) % suggestions.length;
      });
      return;
    }
    if (event.key === "Tab" && !event.shiftKey && listShown) {
      event.preventDefault();
      accept(suggestions[Math.max(active, 0)]!.path);
      return;
    }
    if (event.key === "Enter") {
      event.preventDefault();
      if (listShown && active >= 0) accept(suggestions[active]!.path);
      else void add();
      return;
    }
    if (event.key === "Escape" && listShown) {
      // The first Escape puts the suggestions away; the dialog stays.
      event.preventDefault();
      event.stopPropagation();
      setListHidden(true);
    }
  };

  // A path that is still being typed is not a mistake yet.
  const midTyping =
    status.kind === "problem" &&
    (status.problem === "protected" ||
      (status.problem === "not_found" && suggestions.length > 0));

  return (
    <div className="space-y-4">
      <div className="space-y-1.5">
        <label htmlFor={machineFieldId} className="text-caption font-medium">
          {t(($) => $.resources.remote_machine_label)}
        </label>
        <Select
          items={machines.map((m) => ({ value: m.daemonId, label: m.title }))}
          value={daemonId}
          onValueChange={(next) => {
            if (!next) return;
            setDaemonId(next);
            setChosenMode(null);
            setActive(-1);
            setError(null);
          }}
          disabled={saving}
        >
          <SelectTrigger id={machineFieldId} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {machines.map((m) => (
              <SelectItem key={m.daemonId} value={m.daemonId} disabled={m.taken}>
                <span className="truncate">{m.title}</span>
                {(m.taken || !m.online) && (
                  <span className="shrink-0 text-muted-foreground">
                    {m.taken
                      ? t(($) => $.resources.remote_machine_taken)
                      : t(($) => $.resources.local_offline)}
                  </span>
                )}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="space-y-1.5">
        <label htmlFor={pathFieldId} className="text-caption font-medium">
          {t(($) => $.resources.remote_path_label)}
        </label>
        <Input
          ref={inputRef}
          id={pathFieldId}
          value={text}
          onChange={(event) => edit(event.target.value)}
          onKeyDown={onKeyDown}
          placeholder={t(($) => $.resources.remote_path_placeholder)}
          className="font-mono"
          autoComplete="off"
          autoCapitalize="off"
          autoCorrect="off"
          spellCheck={false}
          disabled={saving}
          role="combobox"
          aria-autocomplete="list"
          aria-expanded={listShown}
          aria-controls={listId}
          aria-activedescendant={
            listShown && active >= 0 ? `${listId}-${active}` : undefined
          }
          aria-invalid={status.kind === "problem" && !midTyping ? true : undefined}
        />
        {listShown && (
          <ul
            id={listId}
            role="listbox"
            aria-label={t(($) => $.resources.remote_suggestions_label)}
            className="max-h-40 overflow-y-auto rounded-md border p-1"
          >
            {suggestions.map((suggestion, index) => (
              <li
                key={suggestion.path}
                id={`${listId}-${index}`}
                role="option"
                aria-selected={index === active}
                ref={
                  index === active
                    ? (node) => node?.scrollIntoView?.({ block: "nearest" })
                    : undefined
                }
                // Keep the caret in the field while a suggestion is clicked.
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => accept(suggestion.path)}
                className={cn(
                  "flex cursor-default items-center gap-2 rounded-sm px-2 py-1 font-mono text-caption hover:bg-accent/70",
                  index === active && "bg-accent",
                )}
              >
                <FolderOpen className="size-3.5 shrink-0 text-muted-foreground" />
                <span className="truncate">{suggestion.label}</span>
              </li>
            ))}
          </ul>
        )}
        <DirectoryStatusLine
          status={status}
          muted={midTyping}
          problemMessage={problemMessage}
        />
      </div>

      <LocalDirectoryModeOptions
        value={mode}
        onChange={setChosenMode}
        unavailableReason={unavailableReason}
      />

      {error && (
        <div
          role="alert"
          className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/5 px-2.5 py-1.5 text-caption text-destructive"
        >
          <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
          <span className="min-w-0 break-words">{error}</span>
        </div>
      )}

      <DialogFooter>
        <Button variant="outline" onClick={onDone} disabled={saving}>
          {t(($) => $.resources.mode_cancel)}
        </Button>
        <Button onClick={() => void add()} disabled={!canAdd}>
          {saving && <Spinner className="size-3.5" />}
          {t(($) => $.resources.mode_add)}
        </Button>
      </DialogFooter>
    </div>
  );
}

function DirectoryStatusLine({
  status,
  muted,
  problemMessage,
}: {
  status: DirectoryStatus;
  muted: boolean;
  problemMessage: (problem: RuntimeDirectoryProblem) => string;
}) {
  const { t } = useT("projects");
  if (status.kind === "empty") return null;
  const line = "flex items-start gap-1.5 text-micro";
  if (status.kind === "reading") {
    return (
      <p className={cn(line, "text-muted-foreground")}>
        <Spinner className="mt-0.5 size-3 shrink-0" />
        {t(($) => $.resources.remote_reading)}
      </p>
    );
  }
  if (status.kind === "unknown") {
    return (
      <p className={cn(line, "text-muted-foreground")}>
        {t(($) => $.resources.remote_unavailable)}
      </p>
    );
  }
  if (status.kind === "problem") {
    return (
      <p className={cn(line, muted ? "text-muted-foreground" : "text-destructive")}>
        {!muted && <TriangleAlert className="mt-0.5 size-3 shrink-0" />}
        {problemMessage(status.problem)}
      </p>
    );
  }
  return (
    <p className={cn(line, "text-muted-foreground")}>
      <Check className="mt-0.5 size-3 shrink-0 text-success" />
      {status.listing.is_git_repo
        ? t(($) => $.resources.remote_status_ok_git)
        : t(($) => $.resources.remote_status_ok)}
    </p>
  );
}
