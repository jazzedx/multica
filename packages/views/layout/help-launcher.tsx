"use client";

import { useRef } from "react";
import {
  ArrowUpRight,
  BookOpen,
  CircleHelp,
  Megaphone,
  MessageCircle,
} from "@multica/ui/icons";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { TooltipHint } from "@multica/ui/components/common/tooltip-hint";
import { useModalStore } from "@multica/core/modals";
import { useConfigStore } from "@multica/core/config";
import { useT } from "../i18n";
import { docsLocalePrefix } from "../common/docs-locale";
import { RAIL_ICON_BUTTON_CLASS } from "./rail-styles";
import { DOCS_URL } from "./help-links";
import { useWhatsNewMenuPanel, WhatsNewMenuPanel } from "../whats-new/whats-new-menu-panel";

export function HelpLauncher() {
  const { t, i18n } = useT("layout");
  const serverVersion = useConfigStore((state) => state.serverVersion);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const whatsNew = useWhatsNewMenuPanel();
  return (
    <>
      <DropdownMenu>
        <TooltipHint label={t(($) => $.help.trigger)} side="right">
          <DropdownMenuTrigger
            ref={triggerRef}
            aria-label={t(($) => $.help.trigger)}
            className={RAIL_ICON_BUTTON_CLASS}
          >
            <CircleHelp />
          </DropdownMenuTrigger>
        </TooltipHint>
        <DropdownMenuContent
          align="end"
          side="right"
          sideOffset={8}
          className="min-w-40 max-w-56"
        >
          <DropdownMenuItem
            render={
              <a
                href={`${DOCS_URL}${docsLocalePrefix(i18n.language)}`}
                target="_blank"
                rel="noopener noreferrer"
              />
            }
          >
            <BookOpen className="h-3.5 w-3.5" />
            {t(($) => $.help.docs)}
            <ArrowUpRight className="size-3 translate-y-px text-faint-foreground" />
          </DropdownMenuItem>
          {whatsNew.available && (
            <DropdownMenuItem onClick={whatsNew.show}>
              <Megaphone className="h-3.5 w-3.5" />
              {t(($) => $.whats_new.trigger)}
              {whatsNew.newestUnread && (
                <span
                  data-slot="whats-new-tag"
                  className="ml-auto text-caption font-medium text-brand tabular-nums"
                >
                  {`v${whatsNew.newestUnread}`}
                </span>
              )}
            </DropdownMenuItem>
          )}
          <DropdownMenuItem
            onClick={() => useModalStore.getState().open("feedback")}
          >
            <MessageCircle className="h-3.5 w-3.5" />
            {t(($) => $.help.feedback)}
          </DropdownMenuItem>
          {serverVersion && (
            <>
              <DropdownMenuSeparator />
              {/* DropdownMenuLabel renders Base UI's Menu.GroupLabel, which reads
                  a Menu.Group context and throws if it has no Group ancestor. It
                  must always be wrapped in a DropdownMenuGroup — without it the
                  Help menu crashes the whole app on open (no error boundary sits
                  above the rail). */}
              <DropdownMenuGroup>
                <DropdownMenuLabel className="font-normal break-words">
                  {t(($) => $.help.server_version, { version: serverVersion })}
                </DropdownMenuLabel>
              </DropdownMenuGroup>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
      <WhatsNewMenuPanel panel={whatsNew} anchor={triggerRef} />
    </>
  );
}
