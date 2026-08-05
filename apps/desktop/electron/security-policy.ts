import path from "node:path";
import { fileURLToPath } from "node:url";

export type SecurityPolicyMode = "dev" | "packaged";

export interface SecurityPolicyOptions {
  mode: SecurityPolicyMode;
  devOrigin: string;
  packagedIndexPath: string;
}

export interface SecurityPolicy {
  readonly mode: SecurityPolicyMode;
  readonly devOrigin: string;
  readonly packagedIndexPath: string;
}

export type SecurityPolicyErrorCode = "UNTRUSTED_SENDER";

export class SecurityPolicyError extends Error {
  readonly code: SecurityPolicyErrorCode;

  constructor(code: SecurityPolicyErrorCode) {
    super(code);
    this.name = "SecurityPolicyError";
    this.code = code;
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

const AMBIGUOUS_PATH_ESCAPES = /%(?:00|2e|2f|5c|25|3a|3f|23|40)/i;
const MALFORMED_ESCAPE = /%(?![0-9a-f]{2})/i;

function hasAmbiguousEncoding(rawURL: string): boolean {
  return MALFORMED_ESCAPE.test(rawURL) || AMBIGUOUS_PATH_ESCAPES.test(rawURL);
}

function canonicalPath(filePath: string): string {
  const normalized = path.normalize(path.resolve(filePath));
  return process.platform === "win32" ? normalized.toLowerCase() : normalized;
}

function parseURL(rawURL: string): URL | null {
  if (
    typeof rawURL !== "string" ||
    rawURL.length === 0 ||
    rawURL.trim() !== rawURL ||
    rawURL.includes("\\") ||
    /[\u0000-\u001f\u007f]/.test(rawURL)
  ) {
    return null;
  }
  if (hasAmbiguousEncoding(rawURL)) return null;

  try {
    const parsed = new URL(rawURL);
    if (parsed.username || parsed.password) return null;
    return parsed;
  } catch {
    return null;
  }
}

function parseDevOrigin(origin: string): string {
  const parsed = parseURL(origin);
  if (!parsed || parsed.protocol !== "http:" || parsed.username || parsed.password) {
    throw new Error("Invalid development origin");
  }
  return parsed.origin;
}

export function createSecurityPolicy(options: SecurityPolicyOptions): SecurityPolicy {
  return {
    mode: options.mode,
    devOrigin: parseDevOrigin(options.devOrigin),
    packagedIndexPath: canonicalPath(options.packagedIndexPath),
  };
}

function isExpectedPackagedFile(policy: SecurityPolicy, parsed: URL, rawURL: string): boolean {
  if (
    parsed.protocol !== "file:" ||
    parsed.hostname ||
    parsed.search ||
    parsed.hash ||
    rawURL.includes("?") ||
    rawURL.includes("#")
  ) {
    return false;
  }
  if (hasAmbiguousEncoding(rawURL)) return false;

  try {
    return canonicalPath(fileURLToPath(parsed)) === policy.packagedIndexPath;
  } catch {
    return false;
  }
}

export function isAllowedNavigation(policy: SecurityPolicy, rawURL: string): boolean {
  const parsed = parseURL(rawURL);
  if (!parsed) return false;

  if (policy.mode === "dev") {
    return parsed.protocol === "http:" && parsed.origin === policy.devOrigin;
  }

  return isExpectedPackagedFile(policy, parsed, rawURL);
}

export function isTrustedSender(
  policy: SecurityPolicy,
  frameURL: string | null | undefined,
): boolean {
  return typeof frameURL === "string" && isAllowedNavigation(policy, frameURL);
}

export function assertTrustedSender(
  policy: SecurityPolicy,
  frameURL: string | null | undefined,
): void {
  if (!isTrustedSender(policy, frameURL)) {
    throw new SecurityPolicyError("UNTRUSTED_SENDER");
  }
}
