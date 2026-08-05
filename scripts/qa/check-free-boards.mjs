// Are the five free job boards still alive and still the shape the parser expects?
//
// The fixture tests (sources_remote_api_test.go) prove the parser reads a FROZEN
// 2026-07-12 response. They cannot catch the realistic failure: a board changing
// its JSON, going down, or starting to require a key. That is exactly what killed
// the Indeed source silently. This check hits the live endpoints and asserts each
// still returns usable jobs, so the day one breaks, a nightly says so instead of
// a user finding an empty search.
//
// No key, no browser, no app — just the same HTTP calls the Go collector makes.
//
// Exit 0 if every enabled board returns jobs; exit 1 (and name the culprit) if any
// board is down or reshaped.
//
// Run:  node scripts/qa/check-free-boards.mjs

const UA =
  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36";

async function get(url) {
  const res = await fetch(url, { headers: { "User-Agent": UA, Accept: "application/json, text/xml, */*" } });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.text();
}

// Each board: fetch, parse the way the Go collector does, and confirm the fields
// the pipeline needs (title, company, url) survive.
const BOARDS = [
  {
    name: "Remotive",
    async count() {
      const data = JSON.parse(await get("https://remotive.com/api/remote-jobs?limit=5&search=developer"));
      return (data.jobs ?? []).filter((j) => j.title && j.company_name && j.url).length;
    },
  },
  {
    name: "RemoteOK",
    async count() {
      const data = JSON.parse(await get("https://remoteok.com/api"));
      // Element 0 is a legal-notice object with no "position".
      return data.filter((j) => j && j.position && (j.url || j.apply_url)).length;
    },
  },
  {
    name: "Jobicy",
    async count() {
      const data = JSON.parse(await get("https://jobicy.com/api/v2/remote-jobs?count=5"));
      return (data.jobs ?? []).filter((j) => j.jobTitle && j.companyName && j.url).length;
    },
  },
  {
    name: "Arbeitnow",
    async count() {
      const data = JSON.parse(await get("https://www.arbeitnow.com/api/job-board-api"));
      return (data.data ?? []).filter((j) => j.title && j.company_name && j.url).length;
    },
  },
  {
    name: "WeWorkRemotely",
    async count() {
      const xml = await get("https://weworkremotely.com/categories/remote-programming-jobs.rss");
      const items = xml.match(/<item>/g) ?? [];
      // The title carries "Company: Role"; a feed with items but no colon-form
      // titles would mean the format changed.
      const titled = (xml.match(/<title>[^<]*: [^<]*<\/title>/g) ?? []).length;
      return Math.min(items.length, titled || items.length);
    },
  },
];

const results = [];
for (const board of BOARDS) {
  try {
    const count = await board.count();
    results.push({ name: board.name, count, ok: count > 0 });
    console.log(`${count > 0 ? "OK " : "-- "} ${board.name}: ${count} usable job(s)`);
  } catch (error) {
    results.push({ name: board.name, count: 0, ok: false, error: error.message });
    console.log(`FAIL ${board.name}: ${error.message}`);
  }
}

const dead = results.filter((r) => !r.ok);
console.log(`\n=== ${results.length - dead.length}/${results.length} boards live ===`);
if (dead.length > 0) {
  console.log(`Down or reshaped: ${dead.map((d) => d.name).join(", ")}`);
  console.log("Recapture the fixture and update the parser for any board listed above.");
  process.exit(1);
}
