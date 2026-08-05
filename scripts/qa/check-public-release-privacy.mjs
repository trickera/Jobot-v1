import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";

const repoRoot = path.resolve(import.meta.dirname, "../..");

const PRIVATE_FORMATS = new Set([
  ".doc",
  ".docx",
  ".odt",
  ".pages",
  ".pdf",
  ".rtf",
  ".xls",
  ".xlsx",
]);

const PRIVATE_FILE_EXTENSIONS = new Set([
  ".cert",
  ".cer",
  ".crt",
  ".db",
  ".db-shm",
  ".db-wal",
  ".dmp",
  ".dump",
  ".der",
  ".exe",
  ".key",
  ".local",
  ".log",
  ".p12",
  ".pem",
  ".pfx",
  ".sqlite",
  ".sqlite3",
]);

const PRIVATE_DIRECTORY_NAMES = new Set([
  ".cache",
  ".agents",
  ".claude",
  ".codex",
  ".idea",
  ".mypy_cache",
  ".npm",
  ".pnpm-store",
  ".pytest_cache",
  ".tools",
  ".venv",
  ".vscode",
  "__pycache__",
  "dist",
  "dist-electron",
  "dist-ssr",
  "graphify-out",
  "node_modules",
  "qa-artifacts",
  "release",
  "resumes",
  "target",
  "tmp",
  "venv",
]);

const PRIVATE_FILE_NAMES = new Set([
  "config.json",
  "history.json",
  "profile.json",
]);

const LOCKFILE_NAMES = new Set([
  "npm-shrinkwrap.json",
  "package-lock.json",
  "pnpm-lock.yaml",
  "yarn.lock",
]);

const RESERVED_DOMAINS = new Set([
  "example.com",
  "example.org",
  "example.test",
]);

const SECRET_ASSIGNMENT_RE = new RegExp(
  String.raw`\b(?:api[_-]?key|client[_-]?secret|password|passwd|private[_-]?key|secret(?:[_-]?key)?|access[_-]?token|auth(?:entication)?[_-]?token)\b\s*(?:=|:|=>)\s*(['"\x60])([^'"\x60\r\n]{8,})\1`,
  "gi",
);
const BEARER_RE = /\bBearer\s+([A-Za-z0-9._~+/=-]{20,})/gi;
const EMAIL_RE = /\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/gi;
const PHONE_RE = /(?<![\w])\+?\d[\d\s().-]{5,}\d(?!\w)/g;
const PRIVATE_KEY_HEADER_RE = /-----BEGIN(?: [A-Z0-9]+)* PRIVATE KEY-----/g;
const TOKEN_SHAPE_RES = [
  /\b(?:AKIA|ASIA)[A-Z0-9]{16}\b/g,
  /\b(?:ghp|gho|ghu|ghs)_[A-Za-z0-9]{20,}\b/g,
  /\bgithub_pat_[A-Za-z0-9_]{20,}\b/g,
  /\bAIza[A-Za-z0-9_-]{30,}\b/g,
  /\bxox[baprs]-[A-Za-z0-9-]{10,}\b/g,
  /\bsk-(?:proj-|live-)?[A-Za-z0-9_-]{20,}\b/g,
  /\bnpm_[A-Za-z0-9]{30,}\b/g,
];

const SCREENSHOT_EXTENSIONS = new Set([".apng", ".bmp", ".gif", ".jpeg", ".jpg", ".png", ".tif", ".tiff", ".webp"]);
const SCREENSHOT_DIRECTORY_NAMES = new Set([
  "captures",
  "evidence",
  "recordings",
  "screenshots",
  "visual-tests",
  "visual-regression",
]);
const SCREENSHOT_NAME_RE = /(?:^|[-_. ])(?:capture|evidence|recording|screen[-_ ]?(?:capture|shot)|screenshot|snapshot|visual[-_ ]?(?:regression|test))(?:[-_. ]|$)/i;

const USER_DIR = "Users";
const HOME_DIR = "home";
const MOUNT_DIR = "mnt";
const UNIX_ABSOLUTE_RE = new RegExp(
  String.raw`(?:^|[\s"'\x60(=,:])(?:\/${HOME_DIR}\/|\/${USER_DIR}\/|\/${MOUNT_DIR}\/[A-Za-z]\/${USER_DIR}\/)[^\s"'<>]+`,
  "g",
);
const WINDOWS_ABSOLUTE_RE = new RegExp(
  String.raw`\b[A-Za-z]:[\\/]+(?:${USER_DIR}|${HOME_DIR})[\\/]+[^\s"'<>]+`,
  "g",
);

class GuardError extends Error {
  constructor(category, key) {
    super(category);
    this.category = category;
    this.key = key;
  }
}

function fingerprint(category, value) {
  return crypto
    .createHash("sha256")
    .update(`${category}\0${String(value).normalize("NFKC").trim().toLowerCase()}`)
    .digest("hex")
    .slice(0, 16);
}

function normalizeRelativePath(value) {
  return String(value)
    .replaceAll("\\", "/")
    .replace(/^\.\//, "")
    .replace(/\/+/g, "/");
}

function finding(relativePath, line, category, value) {
  return {
    path: normalizeRelativePath(relativePath),
    line,
    category,
    fingerprint: fingerprint(category, value),
  };
}

export function formatFinding(item) {
  return `${item.path}:${item.line}/${item.category}/${item.fingerprint}`;
}

function sortFindings(items) {
  const unique = new Map(items.map((item) => [formatFinding(item), item]));
  return [...unique.values()].sort((left, right) => {
    const pathOrder = left.path.localeCompare(right.path, "en", { numeric: false });
    if (pathOrder !== 0) return pathOrder;
    if (left.line !== right.line) return left.line - right.line;
    const categoryOrder = left.category.localeCompare(right.category, "en");
    if (categoryOrder !== 0) return categoryOrder;
    return left.fingerprint.localeCompare(right.fingerprint, "en");
  });
}

function globToRegExp(glob) {
  let expression = "^";
  for (let index = 0; index < glob.length; index += 1) {
    const character = glob[index];
    if (character === "*" && glob[index + 1] === "*") {
      expression += ".*";
      index += 1;
    } else if (character === "*") {
      expression += "[^/]*";
    } else if (character === "?") {
      expression += "[^/]";
    } else {
      expression += character.replace(/[\\^$+?.()|[\]{}]/g, "\\$&");
    }
  }
  return new RegExp(`${expression}$`, "i");
}

function matchesPathPattern(relativePath, pattern) {
  const normalizedPath = normalizeRelativePath(relativePath).toLowerCase();
  const normalizedPattern = normalizeRelativePath(pattern).toLowerCase().replace(/^\//, "");
  if (normalizedPattern.includes("*") || normalizedPattern.includes("?")) {
    return globToRegExp(normalizedPattern).test(normalizedPath);
  }
  return normalizedPath === normalizedPattern || normalizedPath.includes(normalizedPattern);
}

export function parseDenylist(text) {
  if (typeof text !== "string" || text.includes("\0")) {
    throw new GuardError("denylist_error", "invalid_denylist");
  }

  return text
    .split(/\r?\n/)
    .map((raw, index) => ({ raw: raw.trim(), line: index + 1 }))
    .filter(({ raw }) => raw && !raw.startsWith("#"))
    .map(({ raw, line }) => {
      const prefix = raw.match(/^(path|content|literal)\s*:\s*(.*)$/i);
      const kind = prefix ? prefix[1].toLowerCase() : "both";
      const value = (prefix ? prefix[2] : raw).trim();
      if (!value || value.includes("\0")) {
        throw new GuardError("denylist_error", `line_${line}`);
      }
      return { kind, value, line };
    });
}

export function loadDenylist(filePath) {
  try {
    return parseDenylist(fs.readFileSync(filePath, "utf8"));
  } catch (error) {
    if (error instanceof GuardError) throw error;
    throw new GuardError("denylist_error", "read_failed");
  }
}

function isSyntheticPath(relativePath) {
  const normalized = normalizeRelativePath(relativePath).toLowerCase();
  const segments = normalized.split("/");
  const base = segments.at(-1) ?? "";
  return (
    segments.some((segment) => ["contract", "contracts", "fixture", "fixtures", "test", "tests", "__tests__"].includes(segment)) ||
    /(?:^|[._-])(test|spec)(?:[._-]|$)/i.test(base)
  );
}

function isLockfile(relativePath) {
  return LOCKFILE_NAMES.has(normalizeRelativePath(relativePath).split("/").at(-1)?.toLowerCase());
}

function isReservedDomain(domain) {
  const normalized = domain.toLowerCase().replace(/[.,;:)\]}]+$/, "");
  return RESERVED_DOMAINS.has(normalized) || normalized.endsWith(".invalid");
}

function isSafeSecretLiteral(value) {
  const normalized = value.trim().toLowerCase();
  return (
    normalized.includes("process.env") ||
    normalized.includes("import.meta.env") ||
    normalized.includes("${") ||
    /^(?:redacted|placeholder|dummy|sample|example|synthetic|test|changeme|none|null|undefined|\*+|<[^>]+>)$/i.test(normalized)
  );
}

function pathFindings(relativePath, denylist) {
  const normalized = normalizeRelativePath(relativePath);
  const lower = normalized.toLowerCase();
  const base = lower.split("/").at(-1) ?? "";
  const segments = lower.split("/");
  const results = [];

  for (const rule of denylist) {
    if ((rule.kind === "content")) continue;
    if (matchesPathPattern(normalized, rule.value)) {
      results.push(finding(normalized, 0, "denylist", rule.value));
    }
  }

  const privateDirectory = segments.find((segment) => PRIVATE_DIRECTORY_NAMES.has(segment));
  const rootBuildDirectory = /^(?:dist|dist-ssr|dist-electron|target)(?:\/|$)/i.test(lower);
  if (privateDirectory || rootBuildDirectory) {
    results.push(finding(normalized, 0, "private_directory", privateDirectory ?? lower.split("/")[0]));
  }
  if (lower.startsWith("apps/backend-go/bin/") || lower.startsWith("resources/python/") || lower.startsWith("resources/camoufox/")) {
    results.push(finding(normalized, 0, "private_directory", lower.split("/").slice(0, 3).join("/")));
  }
  if (PRIVATE_FILE_NAMES.has(base) || /^\.env(?:\..+)?$/i.test(base) && base !== ".env.example") {
    results.push(finding(normalized, 0, "private_file", base));
  }
  if (/^resume\.[^/]+$/i.test(base) || /^history\.json$/i.test(base)) {
    results.push(finding(normalized, 0, "private_file", base));
  }
  const extension = path.extname(base).toLowerCase();
  if (PRIVATE_FILE_EXTENSIONS.has(extension)) {
    results.push(finding(normalized, 0, "private_file", extension));
  }
  if (PRIVATE_FORMATS.has(extension)) {
    results.push(finding(normalized, 0, "private_format", extension));
  }
  const screenshotDirectory = segments.some((segment) => SCREENSHOT_DIRECTORY_NAMES.has(segment));
  if (SCREENSHOT_EXTENSIONS.has(extension) && (screenshotDirectory || SCREENSHOT_NAME_RE.test(base))) {
    results.push(finding(normalized, 0, "screenshot_evidence", extension));
  }
  return results;
}

function cleanMatch(value) {
  return value.trim().replace(/^[\s"'`(=:,]+|[\s"'`),;]+$/g, "");
}

function contentFindings(relativePath, line, lockfile, synthetic, text) {
  const results = [];
  let match;

  SECRET_ASSIGNMENT_RE.lastIndex = 0;
  while ((match = SECRET_ASSIGNMENT_RE.exec(text)) !== null) {
    const value = match[2];
    if (!isSafeSecretLiteral(value)) results.push(finding(relativePath, line, "credential_literal", value));
  }
  BEARER_RE.lastIndex = 0;
  while ((match = BEARER_RE.exec(text)) !== null) {
    results.push(finding(relativePath, line, "credential_literal", match[1]));
  }
  PRIVATE_KEY_HEADER_RE.lastIndex = 0;
  while ((match = PRIVATE_KEY_HEADER_RE.exec(text)) !== null) {
    results.push(finding(relativePath, line, "private_key_header", match[0]));
  }
  for (const tokenPattern of TOKEN_SHAPE_RES) {
    tokenPattern.lastIndex = 0;
    while ((match = tokenPattern.exec(text)) !== null) {
      results.push(finding(relativePath, line, "token_shape", match[0]));
    }
  }

  if (!lockfile) {
    EMAIL_RE.lastIndex = 0;
    while ((match = EMAIL_RE.exec(text)) !== null) {
      const domain = match[0].slice(match[0].lastIndexOf("@") + 1);
      if (!(synthetic && isReservedDomain(domain))) {
        results.push(finding(relativePath, line, "contact_email", match[0]));
      }
    }

    PHONE_RE.lastIndex = 0;
    while ((match = PHONE_RE.exec(text)) !== null) {
      const value = cleanMatch(match[0]);
      const digits = value.replace(/\D/g, "");
      const phoneContext = value.startsWith("+") || /(?:phone|mobile|telephone|tel|whatsapp|contact)/i.test(text);
      if (digits.length >= 7 && phoneContext && !(synthetic && digits.includes("555"))) {
        results.push(finding(relativePath, line, "contact_phone", value));
      }
    }
  }

  UNIX_ABSOLUTE_RE.lastIndex = 0;
  while ((match = UNIX_ABSOLUTE_RE.exec(text)) !== null) {
    const value = cleanMatch(match[0]);
    if (value) results.push(finding(relativePath, line, "absolute_local_path", value));
  }
  WINDOWS_ABSOLUTE_RE.lastIndex = 0;
  while ((match = WINDOWS_ABSOLUTE_RE.exec(text)) !== null) {
    results.push(finding(relativePath, line, "absolute_local_path", match[0]));
  }
  return results;
}

function ensureInsideRoot(root, relativePath) {
  const normalized = normalizeRelativePath(relativePath);
  const absolute = path.resolve(root, normalized);
  const relative = path.relative(root, absolute);
  if (path.isAbsolute(relative) || relative === ".." || relative.startsWith(`..${path.sep}`)) {
    throw new GuardError("path_error", "outside_root");
  }
  return { normalized, absolute };
}

function applyContentDenylist(results, relativePath, lines, denylist) {
  for (const rule of denylist) {
    if (rule.kind === "path") continue;
    const needle = rule.value.toLowerCase();
    for (let index = 0; index < lines.length; index += 1) {
      if (lines[index].toLowerCase().includes(needle)) {
        results.push(finding(relativePath, index + 1, "denylist", rule.value));
      }
    }
  }
}

export function scanFiles(root, relativeFiles, { denylist = [], excludePath = null } = {}) {
  const results = [];
  const excluded = excludePath ? path.resolve(excludePath) : null;
  const files = [...new Set(relativeFiles.map(normalizeRelativePath))].sort();

  for (const relativePath of files) {
    const { normalized, absolute } = ensureInsideRoot(root, relativePath);
    if (excluded && path.resolve(absolute) === excluded) continue;
    results.push(...pathFindings(normalized, denylist));

    let stat;
    try {
      stat = fs.lstatSync(absolute);
    } catch {
      throw new GuardError("read_error", "stat_failed");
    }
    if (!stat.isFile() || stat.isSymbolicLink()) continue;

    let buffer;
    try {
      buffer = fs.readFileSync(absolute);
    } catch {
      throw new GuardError("read_error", "read_failed");
    }
    if (buffer.includes(0)) continue;
    const text = buffer.toString("utf8");
    const lines = text.split(/\r?\n/);
    const lockfile = isLockfile(normalized);
    const synthetic = isSyntheticPath(normalized);
    for (let index = 0; index < lines.length; index += 1) {
      results.push(...contentFindings(normalized, index + 1, lockfile, synthetic, lines[index]));
    }
    applyContentDenylist(results, normalized, lines, denylist);
  }
  return sortFindings(results);
}

export function listTrackedFiles(root = repoRoot) {
  let output;
  try {
    output = execFileSync("git", ["-C", root, "ls-files", "-z", "--cached"], { encoding: "buffer" });
  } catch {
    throw new GuardError("git_error", "ls_files_failed");
  }
  return output
    .toString("utf8")
    .split("\0")
    .filter(Boolean)
    .map(normalizeRelativePath)
    .sort();
}

export function scanTracked(root = repoRoot, options = {}) {
  return scanFiles(root, listTrackedFiles(root), options);
}

function parseArgs(argv) {
  const options = { selfTest: false, tracked: false, denylistPath: null };
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === "--self-test") {
      options.selfTest = true;
    } else if (argument === "--tracked") {
      options.tracked = true;
    } else if (argument === "--denylist") {
      const value = argv[index + 1];
      if (!value || value.startsWith("--")) throw new GuardError("usage", "missing_denylist");
      options.denylistPath = value;
      index += 1;
    } else if (argument.startsWith("--denylist=")) {
      const value = argument.slice("--denylist=".length);
      if (!value) throw new GuardError("usage", "missing_denylist");
      options.denylistPath = value;
    } else {
      throw new GuardError("usage", "unknown_argument");
    }
  }
  if (options.selfTest && (options.tracked || options.denylistPath)) {
    throw new GuardError("usage", "self_test_options");
  }
  return options;
}

function emitError(category, key, stderr = process.stderr) {
  stderr.write(`error:0/${category}/${fingerprint(category, key)}\n`);
}

export function runSelfTest() {
  const temp = fs.mkdtempSync(path.join(os.tmpdir(), "jobot-public-privacy-"));
  try {
    fs.mkdirSync(path.join(temp, "contracts"), { recursive: true });
    fs.mkdirSync(path.join(temp, "src"), { recursive: true });
    fs.mkdirSync(path.join(temp, "qa-artifacts"), { recursive: true });
    const reservedEmail = ["candidate", "@", "example.com"].join("");
    const syntheticPhone = ["+1", "202", "555", "0101"].join(" ");
    fs.writeFileSync(
      path.join(temp, "contracts", "fixture.test.json"),
      JSON.stringify({ email: reservedEmail, phone: syntheticPhone, url: "https://example.com" }),
    );
    const legacyToken = ["Legacy", "Synthetic", "Identity"].join(" ");
    const secretValue = ["live", "credential", "value", "123456789"].join("-");
    const privateEmail = ["person", "@", "private.example.net"].join("");
    const absoluteHome = ["", "home", "synthetic", "resume.txt"].join("/");
    const privatePhone = ["+55", "11", "98765", "4321"].join(" ");
    fs.writeFileSync(
      path.join(temp, "src", "main.js"),
      [
        `const apiKey = "${secretValue}";`,
        `const localPath = "${absoluteHome}";`,
        `const email = "${privateEmail}";`,
        `const phone = "${privatePhone}";`,
        `const legacy = "${legacyToken}";`,
      ].join("\n"),
    );
    fs.writeFileSync(path.join(temp, "qa-artifacts", "evidence.txt"), "synthetic");
    fs.writeFileSync(path.join(temp, "resume.docx"), Buffer.from([0, 1, 2, 3]));
    const denylist = parseDenylist(`content:${legacyToken}\n`);
    const safe = scanFiles(temp, ["contracts/fixture.test.json"]);
    if (safe.length !== 0) throw new GuardError("self_test", "safe_fixture_rejected");
    const findings = scanFiles(temp, ["src/main.js", "qa-artifacts/evidence.txt", "resume.docx"], { denylist });
    const categories = new Set(findings.map((item) => item.category));
    for (const category of ["credential_literal", "absolute_local_path", "contact_email", "contact_phone", "denylist", "private_directory", "private_format"]) {
      if (!categories.has(category)) throw new GuardError("self_test", `missing_${category}`);
    }
    const formatted = findings.map(formatFinding);
    if (formatted.some((line) => line.includes(secretValue) || line.includes(privateEmail) || line.includes(legacyToken))) {
      throw new GuardError("self_test", "sensitive_output");
    }
    if (formatted.some((line) => !/^.+:\d+\/[a-z_]+\/[a-f0-9]{16}$/.test(line))) {
      throw new GuardError("self_test", "invalid_output");
    }
    const repeated = scanFiles(temp, ["src/main.js", "qa-artifacts/evidence.txt", "resume.docx"], { denylist }).map(formatFinding);
    if (formatted.join("\n") !== repeated.join("\n")) throw new GuardError("self_test", "unstable_fingerprint");
  } finally {
    fs.rmSync(temp, { recursive: true, force: true });
  }
}

export function runCli(argv = process.argv.slice(2), { root = repoRoot, stderr = process.stderr } = {}) {
  let options;
  try {
    options = parseArgs(argv);
    if (options.selfTest) {
      runSelfTest();
      return 0;
    }
    const denylist = options.denylistPath ? loadDenylist(options.denylistPath) : [];
    const findings = options.tracked || !options.selfTest
      ? scanTracked(root, { denylist, excludePath: options.denylistPath })
      : [];
    if (findings.length > 0) stderr.write(`${findings.map(formatFinding).join("\n")}\n`);
    return findings.length > 0 ? 1 : 0;
  } catch (error) {
    if (error instanceof GuardError) emitError(error.category, error.key, stderr);
    else emitError("execution", "unknown_error", stderr);
    return 2;
  }
}

const invokedPath = process.argv[1] ? pathToFileURL(path.resolve(process.argv[1])).href : null;
if (invokedPath && import.meta.url === invokedPath) {
  process.exitCode = runCli();
}
