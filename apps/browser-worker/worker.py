"""Thin stealth fetch transport for Sencia Job.

This worker does ONE job: open URLs with a Camoufox (stealth Firefox) browser
and hand the raw HTML back to the Go backend. All parsing, dating, filtering,
scoring and business logic live in Go.

Protocol: newline-delimited JSON on stdin/stdout. One request per line, one
response per line. Everything else (diagnostics) goes to stderr.

Requests:
  {"cmd": "start", "headless": true}
  {"cmd": "fetch", "url": "...", "waitUntil": "domcontentloaded", "waitForSelector": ""}
  {"cmd": "fetch_gupy", "url": "..."}
  {"cmd": "warm_indeed"}
  {"cmd": "close"}

Responses:
  {"ok": true}
  {"ok": true, "html": "...", "blocked": false}
  {"ok": true, "records": [ {...}, ... ], "html": "..."}
  {"ok": false, "error": "..."}
"""

from __future__ import annotations

import json
import sys

# Capture the real stdout handle BEFORE any third-party import gets a chance
# to write to it, then repoint the global sys.stdout at stderr for the rest
# of the process. This worker's protocol is newline-delimited JSON on
# stdout/stdin (see module docstring); stdout must stay 100% clean.
#
# The concrete trigger: the first time Camoufox() opens with no cached
# browser binary yet (a genuinely fresh profile), camoufox.pkgman.
# CamoufoxFetcher.install() transparently downloads the ~150 MB browser and
# prints plain status text ("Downloading package: ...", "Extracting
# Camoufox: ...") straight to stdout via click.secho, with no argument to
# redirect it. That corrupted this worker's JSON stream and surfaced in Go
# as `worker resposta invalida: invalid character 'D' looking for beginning
# of value` - the "D" of "Downloading". Rather than special-case that one
# call site, every stdout write from here on (ours or any current/future
# dependency's) is redirected to stderr; only respond() below still reaches
# the real stdout pipe Go reads.
_PROTOCOL_STDOUT = sys.stdout
sys.stdout = sys.stderr

if hasattr(_PROTOCOL_STDOUT, "reconfigure"):
    _PROTOCOL_STDOUT.reconfigure(encoding="utf-8")
if hasattr(sys.stderr, "reconfigure"):
    sys.stderr.reconfigure(encoding="utf-8")

ACCEPT_LANGUAGE_PT_BR = "pt-BR,pt;q=0.9,en-US;q=0.8,en;q=0.7"

# Markers that mean "this page is a wall, not results".
#
# The Cloudflare ones were missing, and the cost was the whole Indeed source:
# br.indeed.com now answers a search with a Cloudflare interstitial titled
# "Security Check - Indeed.com". The old list had "security challenge" — one word
# off — so looks_blocked() returned False, Go parsed zero [data-jk] cards out of
# the interstitial, and the app reported "Busca concluida" with a third of its
# sources silently dead.
#
# These are structural tokens from Cloudflare's challenge runtime, not prose. The
# bare word "captcha" is deliberately NOT here: it appears in the text of real job
# postings (security roles), and a marker that fires on a legitimate result page
# would throw away good jobs.
BLOCK_MARKERS = (
    "authwall",
    "security challenge",
    "security verification",
    "px-captcha",
    "g-recaptcha",
    # Cloudflare challenge (Turnstile / "Just a moment" / managed challenge).
    "cf-chl-bypass",
    "_cf_chl_opt",
    "__cf_chl_tk",
    "/challenge-platform/",
    "cf-turnstile",
)

MAX_JOB_ARRAY_DEPTH = 12
MAX_JOB_RECORDS = 400
MAX_CHILDREN_PER_NODE = 8


def log(message: str) -> None:
    print(message, file=sys.stderr, flush=True)


def respond(payload: dict, stream=None) -> None:
    stream = _PROTOCOL_STDOUT if stream is None else stream
    stream.write(json.dumps(payload, ensure_ascii=False) + "\n")
    stream.flush()


def looks_blocked(html: str) -> bool:
    low = (html or "").lower()
    return any(marker in low for marker in BLOCK_MARKERS)


def find_job_array(value) -> list:
    """Recursively find lists of dicts that look like Gupy job records."""
    title_keys = ("name", "jobName", "title")
    url_keys = ("careerPageUrl", "customUrl", "jobUrl", "url")

    def looks_like_job(item) -> bool:
        if not isinstance(item, dict):
            return False
        has_title = any(isinstance(item.get(k), str) and item[k] for k in title_keys)
        if not has_title:
            return False
        has_url = any(
            isinstance(item.get(k), str) and "gupy.io" in item[k] for k in url_keys
        )
        return has_url or item.get("id") is not None

    out: list = []

    def walk(node, depth: int = 0) -> None:
        if depth > MAX_JOB_ARRAY_DEPTH or len(out) >= MAX_JOB_RECORDS:
            return
        if isinstance(node, list):
            if node and looks_like_job(node[0]):
                for item in node:
                    if looks_like_job(item):
                        out.append(item)
                        if len(out) >= MAX_JOB_RECORDS:
                            return
                return
            for child in node[:MAX_CHILDREN_PER_NODE]:
                walk(child, depth + 1)
        elif isinstance(node, dict):
            for child in list(node.values())[:MAX_CHILDREN_PER_NODE]:
                walk(child, depth + 1)

    walk(value)
    return out


def deduplicate_records(records: list[dict]) -> list[dict]:
    seen: set = set()
    out: list[dict] = []
    for job in records:
        key = str(job.get("id") or job.get("careerPageUrl") or job.get("jobUrl") or "")
        if key and key in seen:
            continue
        if key:
            seen.add(key)
        out.append(job)
    return out


class Transport:
    def __init__(self) -> None:
        self._cm = None
        self._browser = None
        self._page = None

    def start(self, headless: bool) -> None:
        from camoufox.sync_api import Camoufox

        log("[browser] launching Camoufox (stealth Firefox, locale pt-BR)...")
        kwargs = {"headless": bool(headless), "geoip": True}
        try:
            self._cm = Camoufox(locale="pt-BR", **kwargs)
        except TypeError:
            log("[browser] locale kwarg unsupported; using header override.")
            self._cm = Camoufox(**kwargs)
        self._browser = self._cm.__enter__()
        self._page = self._browser.new_page()
        try:
            self._page.set_extra_http_headers({"Accept-Language": ACCEPT_LANGUAGE_PT_BR})
        except Exception as exc:
            log(f"[browser] could not set Accept-Language: {exc}")

    def fetch(self, url: str, wait_until: str, wait_for_selector: str) -> dict:
        log(f"[browser] goto {url}")
        self._page.goto(url, timeout=60000, wait_until=wait_until or "domcontentloaded")
        if wait_for_selector:
            try:
                self._page.wait_for_selector(wait_for_selector, timeout=8000)
            except Exception:
                pass
        html = self._page.content()
        return {"ok": True, "html": html, "blocked": looks_blocked(html)}

    def fetch_gupy(self, url: str) -> dict:
        captured: list = []

        def on_response(resp):
            try:
                if resp.status != 200:
                    return
                ctype = (resp.headers or {}).get("content-type", "") or ""
                if "json" not in ctype.lower():
                    return
                ru = resp.url.lower()
                if "gupy" not in ru:
                    return
                if any(skip in ru for skip in (
                    "/toggle", "/feature", "/auth", "/login",
                    "/i18n", "/user", "/translate", "/analytics",
                )):
                    return
                hits = find_job_array(resp.json())
                if hits:
                    captured.extend(hits)
                    log(f"   [gupy-api] captured {len(hits)} jobs from {resp.url[:90]}")
            except Exception:
                pass

        self._page.on("response", on_response)
        try:
            self._page.goto(url, timeout=60000, wait_until="networkidle")
            self._page.wait_for_timeout(2500)
        except Exception as exc:
            log(f"   [gupy] navigation error: {exc}")
        finally:
            try:
                self._page.remove_listener("response", on_response)
            except Exception:
                pass

        html = ""
        try:
            html = self._page.content()
        except Exception:
            pass

        return {"ok": True, "records": deduplicate_records(captured), "html": html}

    def warm_indeed(self) -> dict:
        try:
            log("[indeed] warming session via home page...")
            self._page.goto("https://br.indeed.com/", timeout=30000, wait_until="domcontentloaded")
            self._page.wait_for_timeout(2500)
            for selector in ("#onetrust-accept-btn-handler", 'button[id*="onetrust-accept"]'):
                try:
                    self._page.locator(selector).first.click(timeout=2000)
                    log("[indeed] accepted cookie banner")
                    break
                except Exception:
                    continue
        except Exception as exc:
            log(f"[indeed] warmup failed (continuing): {exc}")
        return {"ok": True}

    def close(self) -> None:
        if self._cm is not None:
            try:
                self._cm.__exit__(None, None, None)
            except Exception:
                pass
        self._cm = None
        self._browser = None
        self._page = None


def dispatch(request: dict, transport: Transport, emit=respond) -> bool:
    """Handle one decoded request; return False after a clean close."""
    cmd = request.get("cmd") if isinstance(request, dict) else None
    try:
        if cmd == "start":
            transport.start(bool(request.get("headless", True)))
            emit({"ok": True})
        elif cmd == "fetch":
            emit(transport.fetch(
                request.get("url", ""),
                request.get("waitUntil", "domcontentloaded"),
                request.get("waitForSelector", ""),
            ))
        elif cmd == "fetch_gupy":
            emit(transport.fetch_gupy(request.get("url", "")))
        elif cmd == "warm_indeed":
            emit(transport.warm_indeed())
        elif cmd == "close":
            transport.close()
            emit({"ok": True})
            return False
        else:
            emit({"ok": False, "error": f"unknown cmd: {cmd}"})
    except Exception as exc:
        log(f"[worker] {cmd} failed: {type(exc).__name__}: {exc}")
        emit({"ok": False, "error": f"{type(exc).__name__}: {exc}"})
    return True


def main(transport=None, input_stream=None, output_stream=None) -> int:
    transport = Transport() if transport is None else transport
    input_stream = sys.stdin if input_stream is None else input_stream
    emit = respond if output_stream is None else lambda payload: respond(payload, output_stream)
    stopped = False
    for line in input_stream:
        line = line.strip()
        if not line:
            continue
        try:
            request = json.loads(line)
        except json.JSONDecodeError as exc:
            emit({"ok": False, "error": f"invalid json: {exc}"})
            continue

        if not dispatch(request, transport, emit):
            stopped = True
            break
    if not stopped:
        transport.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
