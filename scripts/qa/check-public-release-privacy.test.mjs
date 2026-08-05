import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import {
  formatFinding,
  loadDenylist,
  parseDenylist,
  scanFiles,
  scanTracked,
} from "./check-public-release-privacy.mjs";

const testPath = fileURLToPath(import.meta.url);
const guardPath = path.join(path.dirname(testPath), "check-public-release-privacy.mjs");

function tempTree() {
  return fs.mkdtempSync(path.join(os.tmpdir(), "jobot-public-privacy-test-"));
}

function write(root, relativePath, contents) {
  const target = path.join(root, relativePath);
  fs.mkdirSync(path.dirname(target), { recursive: true });
  fs.writeFileSync(target, contents);
}

function runGuard(args) {
  return spawnSync(process.execPath, [guardPath, ...args], {
    cwd: path.dirname(path.dirname(path.dirname(guardPath))),
    encoding: "utf8",
  });
}

test("self-test is clean and emits no diagnostic output", () => {
  const result = runGuard(["--self-test"]);
  assert.equal(result.status, 0);
  assert.equal(result.stdout, "");
  assert.equal(result.stderr, "");
});

test("usage errors use the privacy-safe diagnostic shape", () => {
  const result = runGuard(["--unknown-option"]);
  assert.equal(result.status, 2);
  assert.match(result.stderr.trim(), /^error:0\/[a-z_]+\/[a-f0-9]{16}$/);
});

test("synthetic contracts are clean while public-looking data is reported privately", () => {
  const root = tempTree();
  try {
    const fixtureEmail = ["candidate", "@", "example.com"].join("");
    const fixturePhone = ["+1", "202", "555", "0101"].join(" ");
    write(root, "contracts/fixture.test.json", JSON.stringify({ email: fixtureEmail, phone: fixturePhone }));
    assert.deepEqual(scanFiles(root, ["contracts/fixture.test.json"]), []);

    const secret = ["live", "credential", "value", "123456789"].join("-");
    const privateEmail = ["candidate", "@", "private.example.net"].join("");
    const privatePhone = ["+55", "11", "98765", "4321"].join(" ");
    const localPath = ["", "home", "synthetic", "resume.txt"].join("/");
    write(
      root,
      "src/main.js",
      [
        `const apiKey = "${secret}";`,
        `const email = "${privateEmail}";`,
        `const phone = "${privatePhone}";`,
        `const localPath = "${localPath}";`,
      ].join("\n"),
    );
    write(root, "qa-artifacts/evidence.txt", "synthetic");
    write(root, "resume.docx", "synthetic");

    const findings = scanFiles(root, ["src/main.js", "qa-artifacts/evidence.txt", "resume.docx"]);
    const categories = new Set(findings.map((item) => item.category));
    for (const category of ["credential_literal", "contact_email", "contact_phone", "absolute_local_path", "private_directory", "private_format"]) {
      assert.ok(categories.has(category));
    }

    const output = findings.map(formatFinding).join("\n");
    assert.doesNotMatch(output, new RegExp(secret));
    assert.doesNotMatch(output, new RegExp(privateEmail));
    assert.match(output, /^.+:\d+\/[a-z_]+\/[a-f0-9]{16}(?:\n.+:\d+\/[a-z_]+\/[a-f0-9]{16})*$/);
    assert.deepEqual(
      output,
      scanFiles(root, ["src/main.js", "qa-artifacts/evidence.txt", "resume.docx"]).map(formatFinding).join("\n"),
    );
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test("external denylist matches content and path without exposing its values", () => {
  const root = tempTree();
  try {
    const legacy = ["Legacy", "Synthetic", "Identity"].join(" ");
    write(root, "src/legacy.txt", `marker: ${legacy}`);
    write(root, "src/old-copy.txt", "clean");
    const denylistPath = path.join(root, "denylist.txt");
    fs.writeFileSync(denylistPath, `content:${legacy}\npath:src/old-*.txt\n`);
    const denylist = loadDenylist(denylistPath);
    const findings = scanFiles(root, ["src/legacy.txt", "src/old-copy.txt", "denylist.txt"], {
      denylist,
      excludePath: denylistPath,
    });
    assert.equal(findings.filter((item) => item.category === "denylist").length, 2);
    const output = findings.map(formatFinding).join("\n");
    assert.doesNotMatch(output, new RegExp(legacy));
    assert.equal(output, findings.map(formatFinding).join("\n"));
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test("agent artifacts, sensitive extensions, evidence images, and token shapes are reported privately", () => {
  const root = tempTree();
  try {
    for (const relativePath of [".agents/session.txt", ".claude/public.txt", ".codex/trace.txt", "graphify-out/graph.json", "docs/release/future.txt"]) {
      write(root, relativePath, "synthetic");
    }
    for (const extension of [".log", ".dump", ".dmp", ".pem", ".key", ".crt", ".cer", ".cert", ".p12", ".pfx", ".der"]) {
      write(root, `sensitive/run${extension}`, "synthetic");
    }
    write(root, "src/screenshot-001.png", "synthetic");
    write(root, "evidence/failure.jpg", "synthetic");
    write(root, "assets/logo.png", "synthetic");
    write(root, "assets/icon.svg", "synthetic");

    const privateKeyHeader = ["-----BEGIN", " PRIVATE KEY-----"].join("");
    const awsToken = ["AKIA", "ABCDEFGHIJKLMNOP"].join("");
    const githubToken = ["ghp_", "abcdefghijklmnopqrstuvwxyz123456"].join("");
    const googleToken = ["AIza", "A".repeat(35)].join("");
    const slackToken = ["xoxb-", "A".repeat(20)].join("");
    const openaiToken = ["sk-proj-", "A".repeat(24)].join("");
    const npmToken = ["npm_", "A".repeat(32)].join("");
    write(
      root,
      "src/credential-shapes.txt",
      [privateKeyHeader, awsToken, githubToken, googleToken, slackToken, openaiToken, npmToken].join("\n"),
    );

    const relativePaths = [
      ".agents/session.txt",
      ".claude/public.txt",
      ".codex/trace.txt",
      "graphify-out/graph.json",
      "docs/release/future.txt",
      ...[".log", ".dump", ".dmp", ".pem", ".key", ".crt", ".cer", ".cert", ".p12", ".pfx", ".der"].map((extension) => `sensitive/run${extension}`),
      "src/screenshot-001.png",
      "evidence/failure.jpg",
      "assets/logo.png",
      "assets/icon.svg",
      "src/credential-shapes.txt",
    ];
    const findings = scanFiles(root, relativePaths);
    const categories = new Set(findings.map((item) => item.category));
    for (const category of ["private_directory", "private_file", "screenshot_evidence", "private_key_header", "token_shape"]) {
      assert.ok(categories.has(category));
    }
    for (const relativePath of [".claude/public.txt", "docs/release/future.txt", "graphify-out/graph.json"]) {
      assert.ok(findings.some((item) => item.path === relativePath && item.category === "private_directory"));
    }
    assert.ok(findings.some((item) => item.path === "sensitive/run.cert" && item.category === "private_file"));
    assert.equal(findings.filter((item) => item.path === "assets/logo.png").length, 0);
    assert.equal(findings.filter((item) => item.path === "assets/icon.svg").length, 0);
    const output = findings.map(formatFinding).join("\n");
    for (const sensitiveValue of [privateKeyHeader, awsToken, githubToken, googleToken, slackToken, openaiToken, npmToken]) {
      assert.doesNotMatch(output, new RegExp(sensitiveValue));
    }
    assert.match(output, /^.+:\d+\/[a-z_]+\/[a-f0-9]{16}(?:\n.+:\d+\/[a-z_]+\/[a-f0-9]{16})*$/);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test("lockfile metadata and untracked files do not create privacy findings", () => {
  const root = tempTree();
  try {
    const dependencyEmail = ["maintainer", "@", "dependency.example.net"].join("");
    write(root, "package-lock.json", JSON.stringify({ author: dependencyEmail, version: 1 }));
    write(root, "contracts/sample.test.json", JSON.stringify({ email: ["person", "@", "example.org"].join("") }));
    write(root, "src/untracked.txt", `const local = "${["", "home", "not-tracked", "file"].join("/")}";`);
    assert.deepEqual(scanFiles(root, ["package-lock.json", "contracts/sample.test.json"]), []);

    execFileSync("git", ["init", "-q"], { cwd: root });
    execFileSync("git", ["add", "package-lock.json", "contracts/sample.test.json"], { cwd: root });
    assert.deepEqual(scanTracked(root), []);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test("invalid denylist and missing files are execution errors", () => {
  assert.throws(() => parseDenylist("content:"));
  const root = tempTree();
  try {
    assert.throws(() => scanFiles(root, ["missing.txt"]));
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
