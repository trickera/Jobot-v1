import { describe, expect, it } from "vitest";
import path from "node:path";
import { pathToFileURL } from "node:url";
import {
  SecurityPolicyError,
  createSecurityPolicy,
  isAllowedNavigation,
  isTrustedSender,
  assertTrustedSender,
} from "./security-policy";

const devPolicy = createSecurityPolicy({
  mode: "dev",
  devOrigin: "http://127.0.0.1:1420",
  packagedIndexPath: path.resolve("fixture-app", "dist", "index.html"),
});

const packagedIndexPath = path.resolve("fixture-app", "dist", "index.html");
const packagedPolicy = createSecurityPolicy({
  mode: "packaged",
  devOrigin: "http://127.0.0.1:1420",
  packagedIndexPath,
});
const packagedIndexURL = pathToFileURL(packagedIndexPath).href;

describe("navigation policy", () => {
  it.each([
    ["dev canonical origin", devPolicy, "http://127.0.0.1:1420/", true],
    ["dev path on the same origin", devPolicy, "http://127.0.0.1:1420/@vite/client", true],
    ["dev prefix confusion", devPolicy, "http://127.0.0.1:14200/", false],
    ["dev external origin", devPolicy, "http://127.0.0.2:1420/", false],
    ["dev credentials", devPolicy, "http://user:pass@127.0.0.1:1420/", false],
    ["dev wrong scheme", devPolicy, "https://127.0.0.1:1420/", false],
    ["dev malformed encoding", devPolicy, "http://127.0.0.1:1420/%", false],
    ["dev backslash normalization", devPolicy, "http:\\127.0.0.1:1420/", false],
    ["dev surrounding whitespace", devPolicy, " http://127.0.0.1:1420/", false],
    ["packaged exact file", packagedPolicy, packagedIndexURL, true],
    ["packaged canonical file", packagedPolicy, packagedIndexURL.replace("index.html", "./index.html"), true],
    ["packaged path prefix confusion", packagedPolicy, `${packagedIndexURL}.bak`, false],
    ["packaged outside path", packagedPolicy, pathToFileURL(path.resolve("fixture-app", "other.html")).href, false],
    ["packaged encoded traversal", packagedPolicy, packagedIndexURL.replace("dist/index.html", "dist/%2e%2e/index.html"), false],
    ["packaged encoded separator", packagedPolicy, packagedIndexURL.replace("dist/index.html", "dist%2findex.html"), false],
    ["packaged credentials", packagedPolicy, packagedIndexURL.replace("file://", "file://user:pass@"), false],
    ["packaged host", packagedPolicy, packagedIndexURL.replace("file://", "file://localhost/"), false],
    ["packaged query", packagedPolicy, `${packagedIndexURL}?outside=1`, false],
    ["packaged empty query", packagedPolicy, `${packagedIndexURL}?`, false],
    ["packaged fragment", packagedPolicy, `${packagedIndexURL}#outside`, false],
    ["packaged wrong scheme", packagedPolicy, packagedIndexURL.replace("file:", "http:"), false],
  ])("%s", (_label, policy, url, expected) => {
    expect(isAllowedNavigation(policy, url)).toBe(expected);
  });
});

describe("IPC sender policy", () => {
  it.each([
    ["dev main frame", devPolicy, "http://127.0.0.1:1420/", true],
    ["dev external frame", devPolicy, "http://evil.example/", false],
    ["dev wrong scheme", devPolicy, "file:///tmp/index.html", false],
    ["missing sender frame", devPolicy, null, false],
    ["packaged main frame", packagedPolicy, packagedIndexURL, true],
    ["packaged external frame", packagedPolicy, "https://evil.example/", false],
    ["packaged prefix frame", packagedPolicy, `${packagedIndexURL}.bak`, false],
  ])("%s", (_label, policy, frameURL, expected) => {
    expect(isTrustedSender(policy, frameURL)).toBe(expected);
  });

  it("returns a typed, path-free error for an untrusted sender", () => {
    try {
      assertTrustedSender(packagedPolicy, "file:///tmp/elsewhere.html");
      throw new Error("expected assertTrustedSender to throw");
    } catch (error) {
      expect(error).toBeInstanceOf(SecurityPolicyError);
      expect(error).toMatchObject({ code: "UNTRUSTED_SENDER" });
      expect(String(error)).not.toContain("tmp");
      expect(String(error)).not.toContain("fixture-app");
    }
  });
});
