// Product telemetry is off in a private deployment. The exports keep their
// names so callers stay unchanged.

export const EVENT_SCHEMA_VERSION = 2;

export interface AnalyticsConfig {
  key: string;
  host: string;
  appVersion?: string;
  environment?: string;
}

export type ClientType = "desktop" | "web";

export function detectClientType(): ClientType {
  return "web";
}

export function initAnalytics(_config: AnalyticsConfig | null | undefined): boolean {
  return false;
}

export function identify(_userId: string, _userProperties?: Record<string, unknown>): void {}

export function resetAnalytics(): void {}

export interface CaptureEventOptions {
  sendInstantly?: boolean;
  onCaptured?: () => void;
}

export function captureEvent(
  _name: string,
  _props?: Record<string, unknown>,
  options?: CaptureEventOptions,
): void {
  options?.onCaptured?.();
}

export function captureException(_error: unknown, _props?: Record<string, unknown>): void {}

export function setPersonProperties(_props: Record<string, unknown>): void {}

export function captureSignupSource(): void {}
