import { NextResponse, type NextRequest } from "next/server";
import { isLandingPath, splitLandingLocale } from "./landing-locale-path";

/**
 * Marketing pages (including the /zh, /ko and /ja language prefixes, and
 * download / licensing) redirect to the last workspace, or to /login when
 * there is none.
 * Upstream's LANDING_ROUTES decides what counts as a marketing page, so a
 * page added upstream is hidden without a change here.
 */
export function redirectHiddenLanding(
  req: NextRequest,
  hasSession: boolean,
  lastSlug: string | undefined,
): NextResponse | null {
  const { pathname } = req.nextUrl;
  const prefixed = splitLandingLocale(pathname);
  // A workspace that took a language prefix as its slug before the prefix was
  // reserved stays reachable at its root path for its members.
  if (prefixed?.path === "/" && hasSession && lastSlug === pathname.slice(1)) return null;
  const page = prefixed?.path ?? pathname;
  if (!isLandingPath(page)) return null;

  const url = req.nextUrl.clone();
  url.pathname = hasSession && lastSlug ? `/${lastSlug}/issues` : "/login";
  return NextResponse.redirect(url);
}
