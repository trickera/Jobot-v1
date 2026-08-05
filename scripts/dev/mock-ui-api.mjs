import http from "node:http";

const token = process.env.SENCIA_MOCK_TOKEN ?? "mini-test";
const port = Number(process.env.SENCIA_MOCK_PORT ?? 48731);
const emptyMode = process.env.SENCIA_MOCK_EMPTY === "1";

const demoJobs = [
  {
    id: "demo-linkedin-1",
    source: "LinkedIn",
    title: "Product Designer Pleno (Remoto)",
    company: "Northwind Studio",
    location: "Brazil (Remoto)",
    url: "https://www.linkedin.com/jobs/view/demo-1",
    status: "[APPLY NOW]",
    score: 86,
    profile: "Product Designer",
    missingKeywords: ["Design System", "Pesquisa com usuarios", "Prototipagem"],
    description: [
      "Somos um estudio de produto digital focado em experiencias web.",
      "",
      "Principais responsabilidades:",
      "- Conduzir descoberta e entrevistas com usuarios",
      "- Desenhar fluxos, wireframes e prototipos navegaveis",
      "- Manter e evoluir o design system do produto",
      "",
      "Requisitos:",
      "- Experiencia com Figma e prototipagem",
      "- Portfolio com casos de produto ponta a ponta",
      "- Ingles intermediario",
      "",
      "Beneficios:",
      "- VR, VA, plano de saude, remoto full",
    ].join("\n"),
  },
  {
    id: "demo-indeed-1",
    source: "Indeed",
    title: "UX Designer - Pleno",
    company: "Contoso Digital",
    location: "Porto Alegre, RS (Hibrido)",
    url: "https://br.indeed.com/viewjob?jk=demo-2",
    status: "[ADJUST RESUME]",
    score: 72,
    profile: "UX Designer",
    missingKeywords: ["Design System", "Acessibilidade"],
    description: [
      "Buscamos profissional para evoluir a experiencia do nosso app.",
      "",
      "O que voce fara:",
      "- Traduzir pesquisa em fluxos e telas",
      "- Apoiar squads com testes de usabilidade",
      "",
      "Requisitos:",
      "- 3+ anos em UX ou Product Design",
      "- Conhecimento em acessibilidade e design system",
    ].join("\n"),
  },
];

const demoLowScoreJobs = [
  {
    ...demoJobs[0],
    id: "demo-linkedin-low-1",
    title: "Design Operations Analyst",
    company: "Fabrikam Labs",
    url: "https://www.linkedin.com/jobs/view/demo-low-1",
    status: "[DISCARD]",
    score: 54,
    missingKeywords: ["Design Ops", "Figma Branching"],
  },
  {
    ...demoJobs[1],
    id: "demo-indeed-low-1",
    title: "Junior Visual Designer",
    company: "Adventure Works",
    url: "https://br.indeed.com/viewjob?jk=demo-low-2",
    status: "[DISCARD]",
    score: 41,
    missingKeywords: ["Design System", "Prototipagem", "UX Writing"],
  },
];

const demoSavedJobs = demoJobs.map((job, index) => ({
  ...job,
  savedAt: new Date(Date.now() - (index + 1) * 60 * 60 * 1000).toISOString(),
  scoreSource: index === 0 ? "ai" : "offline_no_key",
}));

const demoApplications = [
  {
    id: "application:demo-linkedin-1",
    jobId: demoJobs[0].id,
    status: "applied",
    createdAt: new Date(Date.now() - 26 * 60 * 60 * 1000).toISOString(),
    updatedAt: new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString(),
    job: { ...demoJobs[0], scoreSource: "ai" },
  },
];

const demoHistory = [
  {
    id: "history-1",
    query: "Product Designer",
    filters: { workMode: "remote", location: "Brazil", recentHours: 24 },
    resultsCount: 6,
    createdAt: new Date(Date.now() - 3 * 60 * 60 * 1000).toISOString(),
  },
  {
    id: "history-2",
    query: "UX Designer",
    filters: { workMode: "hybrid", location: "Porto Alegre", recentHours: 48 },
    resultsCount: 3,
    createdAt: new Date(Date.now() - 28 * 60 * 60 * 1000).toISOString(),
  },
];

const emptyDiagnostics = {
  collected: 18,
  fresh: 16,
  evaluated: 5,
  approved: 0,
  discarded: 4,
  dropped: 7,
  skippedNoDescription: 4,
  suggestions: [
    "Reduza o match threshold ou amplie as keywords editaveis do curriculo.",
    "Revise senioridade, maximo de anos, modalidade e blacklist.",
    "Algumas vagas nao abriram descricao completa; tente novamente ou reduza maxJobs para dar mais tempo por vaga.",
  ],
  sources: {
    LinkedIn: { collected: 8, fresh: 8, evaluated: 3, approved: 0, discarded: 2, dropped: 3, skippedNoDescription: 0 },
    Indeed: { collected: 10, fresh: 8, evaluated: 2, approved: 0, discarded: 2, dropped: 4, skippedNoDescription: 4 },
  },
};

let state = {
  running: false,
  message: "Pronto",
  jobs: [],
  lowScoreJobs: [],
  total: 0,
  diagnostics: undefined,
};

function resetState() {
  state = { running: false, message: "Sessao limpa", jobs: [], lowScoreJobs: [], total: 0, diagnostics: undefined };
}

function startDemoSearch() {
  state.running = true;
  state.message = "Buscando vagas (demo)...";
  state.jobs = [];
  state.lowScoreJobs = [];
  state.total = 0;
  state.diagnostics = undefined;
  setTimeout(() => {
    state.running = false;
    if (emptyMode) {
      state.jobs = [];
      state.lowScoreJobs = [];
      state.total = 0;
      state.message = "Busca concluida: 0 vagas encontradas.";
      state.diagnostics = emptyDiagnostics;
      return;
    }
    state.jobs = demoJobs;
    state.lowScoreJobs = demoLowScoreJobs;
    state.total = demoJobs.length;
    state.message = `Demo concluida — ${demoJobs.length} vagas`;
  }, 800);
}

const routes = {
  "GET /health": () => ({ status: "ok" }),
  "GET /api/v1/state": () => ({ service: "sencia-job", status: state.running ? "running" : "ready", version: "0.1.0", jobs: state.jobs.length, saved: 0, applications: 0, sources: 2 }),
  "GET /api/v1/search/status": () => ({ running: state.running, message: state.message, total: state.total, jobs: state.jobs, lowScoreJobs: state.lowScoreJobs, diagnostics: state.diagnostics }),
  "GET /api/v1/search/plan": () => ({
    roles: ["Product Designer", "UX Designer"],
    rolesSource: "role",
    ignoredRoles: [],
    levels: ["Junior", "Pleno", "Senior"],
    excludedLevels: ["Director"],
    scoringTerms: ["Figma", "Design System", "Prototipagem", "Pesquisa"],
    scoringSource: "resume",
    staleKeywords: false,
    keywordsForRoles: ["Product Designer", "UX Designer"],
    workMode: "remote",
    locations: [{ location: "Brazil", remote: true }],
    sources: ["LinkedIn", "Indeed"],
    summary: "Product and UX Design roles, remote in Brazil",
  }),
  "POST /api/v1/search/reset": () => {
    resetState();
    return { message: "reset ok" };
  },
  "POST /api/v1/search": () => {
    startDemoSearch();
    return { message: "accepted" };
  },
  "GET /api/v1/logs": () => ({ logs: [{ id: 1, ts: new Date().toISOString(), level: "info", message: "Demo UI fixture loaded" }] }),
  "GET /api/v1/jobs/saved": () => ({ jobs: demoSavedJobs }),
  "GET /api/v1/applications": () => ({ applications: demoApplications }),
  "GET /api/v1/history": () => ({ history: demoHistory }),
  "POST /api/v1/notifications/drain": () => ({ jobs: [] }),
  "POST /api/v1/open-url": () => ({ ok: true, message: "demo url accepted" }),
  "POST /api/v1/jobs/action": () => ({ ok: true, message: "demo action accepted" }),
  "GET /api/v1/config": () => ({ version: 1, form: {}, toggles: {}, localItems: { jobs: 2, saved: 2, applications: 1, history: 2 } }),
  "PUT /api/v1/config": () => ({ ok: true }),
  "GET /api/v1/ai/usage": () => ({ day: new Date().toISOString().slice(0, 10), mode: "free_economy", consent: false, requests: 8, cacheHits: 3, budget: 40, remaining: 32, operationBudgets: { search_score: 20, resume_parse: 5 }, breakdown: [] }),
  "GET /api/v1/browser/health": () => ({ pythonFound: true, workerFound: true, camoufoxImportable: true, browserInstalled: true, browserBundled: true, browserSource: "bundled", message: "Browser worker pronto" }),
  "GET /api/v1/health/install": () => ({ ok: true, packaged: false, repairAvailable: false, message: "Ambiente de desenvolvimento pronto", checks: [{ id: "backend", ok: true, label: "Backend local" }, { id: "browser", ok: true, label: "Browser worker" }] }),
  "GET /api/v1/ocr/status": () => ({ installed: false, installing: false }),
};

function authOk(req) {
  const header = req.headers.authorization ?? "";
  return header === `Bearer ${token}` || header === "Bearer sencia-dev";
}

const server = http.createServer((req, res) => {
  if (req.method === "OPTIONS") {
    res.writeHead(204, {
      "Access-Control-Allow-Origin": "*",
      "Access-Control-Allow-Headers": "Authorization, Content-Type",
      "Access-Control-Allow-Methods": "GET,POST,PUT,OPTIONS",
    });
    res.end();
    return;
  }

  if (!authOk(req)) {
    res.writeHead(401, { "Content-Type": "application/json", "Access-Control-Allow-Origin": "*" });
    res.end(JSON.stringify({ message: "unauthorized" }));
    return;
  }

  const key = `${req.method} ${req.url?.split("?")[0] ?? ""}`;
  const handler = routes[key];
  if (!handler) {
    res.writeHead(404, { "Content-Type": "application/json", "Access-Control-Allow-Origin": "*" });
    res.end(JSON.stringify({ message: "not found" }));
    return;
  }

  res.writeHead(200, {
    "Content-Type": "application/json",
    "Access-Control-Allow-Origin": "*",
  });
  res.end(JSON.stringify(handler()));
});

server.listen(port, "127.0.0.1", () => {
  console.log(`mock ui api on http://127.0.0.1:${port} token=${token}`);
});
