"use client";

import { useState, type ReactNode } from "react";
import {
  ChevronDown,
  ChevronRight,
  Laptop,
  Loader2,
  Lock,
  Monitor,
  Plus,
  SquareTerminal,
  TriangleAlert,
} from "@multica/ui/icons";
import type { QoderConnection } from "@multica/core/runtimes/qoder";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import { ProviderLogo } from "../../runtimes/components/provider-logo";
import { HealthDot, useHealthLabel } from "../../runtimes/components/shared";
import {
  filterDevice,
  HARNESS_PREVIEW_COUNT,
  LOGO_STACK_LIMIT,
  previewOptions,
  type DeviceOption,
  type HarnessOption,
} from "./runtime-choice";

export type QoderChoiceState =
  | "not_configured"
  | "starting"
  | "ready"
  | "error"
  | "stopped";

export function qoderChoiceState(
  connection: QoderConnection,
  runtimeOnline: boolean,
): QoderChoiceState {
  if (!connection.available || !connection.configured) return "not_configured";
  if (!connection.enabled) return "stopped";
  if (connection.status === "error") return "error";
  return runtimeOnline && connection.status === "online" ? "ready" : "starting";
}

const ROW = "flex w-full min-w-0 items-center gap-3 px-4 text-left";
const INTERACTIVE =
  "transition-colors hover:bg-accent/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring";
const SELECTED = "bg-brand/5 shadow-[inset_2px_0_0_var(--brand)] hover:bg-brand/5";
/** Lines child rows up under their machine's tile, past the chevron. */
const CHILD_INDENT = "pl-11";

function Highlight({ text, query }: { text: string; query: string }) {
  const q = query.trim().toLowerCase();
  const index = q ? text.toLowerCase().indexOf(q) : -1;
  if (index < 0) return <>{text}</>;
  return (
    <>
      {text.slice(0, index)}
      <mark className="rounded-xs bg-warning/25 text-inherit">
        {text.slice(index, index + q.length)}
      </mark>
      {text.slice(index + q.length)}
    </>
  );
}

function Radio({ checked, disabled }: { checked: boolean; disabled?: boolean }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "flex size-4 shrink-0 items-center justify-center rounded-full border-[1.5px] border-input",
        checked && "border-brand",
        disabled && "opacity-50",
      )}
    >
      {checked ? <span className="size-2 rounded-full bg-brand" /> : null}
    </span>
  );
}

function Tile({ children, dot }: { children: ReactNode; dot?: ReactNode }) {
  return (
    <span className="relative flex size-7 shrink-0 items-center justify-center rounded-md border bg-background">
      {children}
      {dot}
    </span>
  );
}

const TILE_DOT = "absolute -bottom-0.5 -right-0.5 ring-2 ring-card";

function GroupHeader({
  icon: Icon,
  title,
  count,
}: {
  icon: typeof Monitor;
  title: string;
  count: number;
}) {
  return (
    <div className="flex h-9 items-center gap-2 bg-muted/40 px-4 text-caption">
      <Icon aria-hidden="true" className="size-3.5 text-muted-foreground" />
      <h3 className="font-semibold">{title}</h3>
      <span className="font-mono tabular-nums text-muted-foreground">{count}</span>
    </div>
  );
}

function Badge({
  tone = "muted",
  children,
}: {
  tone?: "muted" | "success" | "warning" | "destructive";
  children: ReactNode;
}) {
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center gap-1 rounded-xs px-1.5 py-0.5 text-micro font-medium",
        tone === "muted" && "bg-muted text-muted-foreground",
        tone === "success" && "bg-success/10 text-success",
        tone === "warning" && "bg-warning/10 text-warning",
        tone === "destructive" && "bg-destructive/10 text-destructive",
      )}
    >
      {children}
    </span>
  );
}

function HarnessRow({
  option,
  query,
  selected,
  indent,
  onSelect,
}: {
  option: HarnessOption;
  query: string;
  selected: boolean;
  indent: boolean;
  onSelect: () => void;
}) {
  const { t } = useT("agents");
  const online = option.runtime.status === "online";
  return (
    <button
      type="button"
      aria-pressed={selected}
      disabled={!online}
      onClick={onSelect}
      className={cn(
        ROW,
        INTERACTIVE,
        "min-h-11",
        indent && CHILD_INDENT,
        "disabled:cursor-not-allowed disabled:text-muted-foreground disabled:hover:bg-transparent",
        selected && SELECTED,
      )}
    >
      <Radio checked={selected} disabled={!online} />
      <Tile>
        <ProviderLogo
          provider={option.runtime.provider}
          className={cn("size-4", !online && "opacity-50")}
        />
      </Tile>
      <span className={cn("min-w-0 truncate text-body", selected && "font-medium")}>
        <Highlight text={option.label} query={query} />
      </span>
      {option.custom ? (
        <Badge>{t(($) => $.creation_studio.runtime_step.custom_badge)}</Badge>
      ) : null}
      {option.version ? (
        <span className="shrink-0 font-mono text-caption text-faint-foreground">
          {option.version}
        </span>
      ) : null}
    </button>
  );
}

function LogoStack({ options }: { options: HarnessOption[] }) {
  const shown = options.slice(0, LOGO_STACK_LIMIT);
  const extra = options.length - shown.length;
  return (
    <span aria-hidden="true" className="hidden items-center pr-1 sm:flex">
      {shown.map((option) => (
        <span
          key={option.runtime.id}
          className="-mr-1 flex size-5 items-center justify-center rounded-sm bg-background ring-1 ring-border"
        >
          <ProviderLogo provider={option.runtime.provider} className="size-3" />
        </span>
      ))}
      {extra > 0 ? (
        <span className="ml-2 font-mono text-micro text-muted-foreground">+{extra}</span>
      ) : null}
    </span>
  );
}

function MachineBlock({
  device,
  open,
  onToggle,
  showAll,
  onShowAll,
  starting,
  query,
  owner,
  selectedRuntimeId,
  selectedNotice,
  onSelect,
}: {
  device: DeviceOption;
  open: boolean;
  onToggle: () => void;
  showAll: boolean;
  onShowAll: () => void;
  starting: boolean;
  query: string;
  owner: string | null;
  selectedRuntimeId: string | null;
  selectedNotice: string | null;
  onSelect: (runtimeId: string) => void;
}) {
  const { t } = useT("agents");
  const healthLabel = useHealthLabel();
  const { machine, harnesses } = device;
  const searching = query.trim().length > 0;
  const offline = machine.health !== "online";
  const Icon = machine.isCurrent ? Laptop : Monitor;
  const subtitle = [owner, machine.deviceSummary].filter(Boolean).join(" · ");
  const { shown, hiddenCount } =
    searching || showAll
      ? { shown: harnesses, hiddenCount: 0 }
      : previewOptions(harnesses, selectedRuntimeId, HARNESS_PREVIEW_COUNT);
  const hiddenNames = harnesses
    .filter((option) => !shown.includes(option))
    .map((option) => option.label)
    .join(", ");

  return (
    <>
      <button
        type="button"
        aria-expanded={open}
        onClick={onToggle}
        className={cn(ROW, INTERACTIVE, "min-h-11", open && "bg-muted/20")}
      >
        <ChevronRight
          aria-hidden="true"
          className={cn(
            "size-3.5 shrink-0 text-muted-foreground transition-transform",
            open && "rotate-90",
          )}
        />
        <Tile dot={<HealthDot health={machine.health} className={TILE_DOT} />}>
          <Icon aria-hidden="true" className="size-3.5 text-muted-foreground" />
        </Tile>
        <span
          className={cn(
            "max-w-64 shrink-0 truncate text-body font-medium",
            offline && "text-muted-foreground",
          )}
        >
          <Highlight text={machine.title} query={query} />
        </span>
        {machine.isCurrent ? (
          <Badge>{t(($) => $.creation_studio.runtime_step.this_machine)}</Badge>
        ) : subtitle ? (
          <span className="min-w-0 truncate text-caption text-muted-foreground">
            {subtitle}
          </span>
        ) : null}
        <span className="ml-auto flex shrink-0 items-center gap-4 text-caption text-muted-foreground">
          {!open && harnesses.length > 0 ? <LogoStack options={harnesses} /> : null}
          <span className="hidden tabular-nums sm:inline">
            {t(($) => $.creation_studio.runtime_step.harness_count, {
              count: harnesses.length,
            })}
          </span>
          <span className="flex min-w-12 items-center justify-end gap-1.5 whitespace-nowrap">
            <HealthDot health={machine.health} />
            {healthLabel(machine.health)}
          </span>
        </span>
      </button>
      {open && harnesses.length === 0 ? (
        <div className={cn(ROW, CHILD_INDENT, "gap-2.5 py-3")}>
          {starting ? (
            <Loader2
              aria-hidden="true"
              className="size-4 shrink-0 animate-spin text-muted-foreground"
            />
          ) : (
            <SquareTerminal
              aria-hidden="true"
              className="size-4 shrink-0 text-muted-foreground"
            />
          )}
          <span className="min-w-0">
            <span className="block text-label font-medium">
              {starting
                ? t(($) => $.creation_studio.runtime_step.local_starting)
                : t(($) => $.creation_studio.runtime_step.local_empty_title)}
            </span>
            <span className="block text-caption text-muted-foreground">
              {t(($) => $.creation_studio.runtime_step.local_empty_hint)}
            </span>
          </span>
        </div>
      ) : null}
      {open
        ? shown.map((option) => {
            const selected = option.runtime.id === selectedRuntimeId;
            return (
              <div key={option.runtime.id}>
                <HarnessRow
                  option={option}
                  query={query}
                  selected={selected}
                  indent
                  onSelect={() => onSelect(option.runtime.id)}
                />
                {selected && selectedNotice ? (
                  <p
                    className={cn(
                      ROW,
                      CHILD_INDENT,
                      SELECTED,
                      "items-start gap-2 pb-2.5 text-caption text-muted-foreground",
                    )}
                  >
                    <TriangleAlert
                      aria-hidden="true"
                      className="mt-0.5 size-3.5 shrink-0 text-warning"
                    />
                    {selectedNotice}
                  </p>
                ) : null}
              </div>
            );
          })
        : null}
      {open && hiddenCount > 0 ? (
        <button
          type="button"
          onClick={onShowAll}
          className={cn(
            ROW,
            INTERACTIVE,
            CHILD_INDENT,
            "h-9 gap-1.5 text-caption text-muted-foreground hover:text-foreground",
          )}
        >
          <ChevronDown aria-hidden="true" className="size-3.5 shrink-0" />
          <span className="truncate">
            {t(($) => $.creation_studio.runtime_step.more_harnesses, {
              count: hiddenCount,
              names: hiddenNames,
            })}
          </span>
        </button>
      ) : null}
    </>
  );
}

export interface RuntimeChoiceListProps {
  local: DeviceOption | null;
  localStarting: boolean;
  remotes: DeviceOption[];
  hiddenRemoteCount: number;
  selectedRuntimeId: string | null;
  query: string;
  currentUserId: string | null;
  ownerName: (ownerId: string | null) => string | null;
  onSelect: (runtimeId: string) => void;
  onAddRemote: () => void;
}

/**
 * Every place an agent can run, as one list of machines. This device is first
 * and open. A machine folds to a single row until opened.
 */
export function RuntimeChoiceList({
  local,
  localStarting,
  remotes,
  hiddenRemoteCount,
  selectedRuntimeId,
  query,
  currentUserId,
  ownerName,
  onSelect,
  onAddRemote,
}: RuntimeChoiceListProps) {
  const { t } = useT("agents");
  const [openOverrides, setOpenOverrides] = useState<ReadonlyMap<string, boolean>>(
    () => new Map(),
  );
  const [showAllIds, setShowAllIds] = useState<ReadonlySet<string>>(() => new Set());
  const searching = query.trim().length > 0;

  const devices = [...(local ? [local] : []), ...remotes];
  const matchedDevices = searching
    ? devices
        .map((device) => filterDevice(device, query))
        .filter((device): device is DeviceOption => device !== null)
    : devices;
  const showMachines = matchedDevices.length > 0 || !searching;

  if (!showMachines) {
    return (
      <div className="rounded-lg border border-dashed px-4 py-6 text-center text-label text-muted-foreground">
        {t(($) => $.creation_studio.runtime_step.no_match, { query })}
      </div>
    );
  }

  const isOpen = (device: DeviceOption) =>
    searching ||
    (openOverrides.get(device.machine.id) ??
      (device.machine.isCurrent ||
        device.harnesses.some((option) => option.runtime.id === selectedRuntimeId)));

  const noticeFor = (device: DeviceOption) => {
    if (device.machine.isCurrent) return null;
    const selected = device.harnesses.find(
      (option) => option.runtime.id === selectedRuntimeId,
    );
    const ownerId = selected?.runtime.owner_id ?? null;
    if (!selected || !ownerId || ownerId === currentUserId) return null;
    const owner = ownerName(ownerId);
    return owner
      ? t(($) => $.creation_studio.runtime_step.remote_warning, { owner })
      : null;
  };

  return (
    <div className="divide-y overflow-hidden rounded-lg border bg-card">
      {showMachines ? (
        <>
          <GroupHeader
            icon={Monitor}
            title={t(($) => $.creation_studio.runtime_step.groups.machines)}
            count={matchedDevices.length}
          />
          {matchedDevices.map((device) => {
            const id = device.machine.id;
            const ownerId = device.harnesses[0]?.runtime.owner_id ?? null;
            const owner = device.machine.isCurrent
              ? null
              : ownerId && ownerId === currentUserId
                ? t(($) => $.creation_studio.runtime_step.owner_you)
                : ownerName(ownerId);
            return (
              <MachineBlock
                key={id}
                device={device}
                open={isOpen(device)}
                onToggle={() =>
                  setOpenOverrides((current) =>
                    new Map(current).set(id, !isOpen(device)),
                  )
                }
                showAll={showAllIds.has(id)}
                onShowAll={() => setShowAllIds((current) => new Set(current).add(id))}
                starting={device.machine.isCurrent && localStarting}
                query={query}
                owner={owner}
                selectedRuntimeId={selectedRuntimeId}
                selectedNotice={noticeFor(device)}
                onSelect={onSelect}
              />
            );
          })}
          {!searching ? (
            <div className={cn(ROW, "h-10 text-caption text-muted-foreground")}>
              {hiddenRemoteCount > 0 ? (
                <span className="flex min-w-0 items-center gap-1.5">
                  <Lock aria-hidden="true" className="size-3 shrink-0" />
                  <span className="truncate">
                    {t(($) => $.creation_studio.runtime_step.hidden_remote, {
                      count: hiddenRemoteCount,
                    })}
                  </span>
                </span>
              ) : null}
              <button
                type="button"
                onClick={onAddRemote}
                className="ml-auto inline-flex shrink-0 items-center gap-1 rounded-sm font-medium text-brand hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <Plus aria-hidden="true" className="size-3.5" />
                {t(($) => $.creation_studio.runtime_step.add_remote)}
              </button>
            </div>
          ) : null}
        </>
      ) : null}
    </div>
  );
}
