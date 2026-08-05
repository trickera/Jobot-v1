export type DescriptionSection = {
  title: string;
  body: string;
};

const sectionPatterns: Array<{ title: string; pattern: RegExp }> = [
  { title: "Principais responsabilidades", pattern: /(?:principais?\s+(?:desafios|responsabilidades|atividades)|o que voce far[aá]|suas atividades)/i },
  { title: "Requisitos", pattern: /(?:requisitos?|o que [eé] essencial|qualifica[cç][oõ]es?|o que buscamos)/i },
  { title: "Diferenciais", pattern: /(?:diferenciais?|nice to have|voce se destaca|desej[aá]vel)/i },
  { title: "Beneficios", pattern: /(?:benef[ií]cios?|o que oferecemos|nossa proposta)/i },
];

function normalizeHeading(line: string) {
  return line.replace(/[:：]\s*$/, "").trim();
}

export function parseDescriptionSections(description: string): DescriptionSection[] {
  const text = description.trim();
  if (!text) return [];

  const lines = text.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
  if (lines.length <= 1) {
    return [{ title: "Sobre a vaga", body: text }];
  }

  const sections: DescriptionSection[] = [];
  let currentTitle = "Sobre a vaga";
  let currentLines: string[] = [];

  function flush() {
    const body = currentLines.join("\n").trim();
    if (body) sections.push({ title: currentTitle, body });
    currentLines = [];
  }

  for (const line of lines) {
    const heading = normalizeHeading(line);
    const match = sectionPatterns.find(({ pattern }) => pattern.test(heading) && heading.length < 90);
    if (match && (heading.endsWith(":") || heading.length < 60)) {
      flush();
      currentTitle = match.title;
      continue;
    }
    currentLines.push(line);
  }
  flush();

  return sections.length > 0 ? sections : [{ title: "Sobre a vaga", body: text }];
}

export function formatDescriptionBody(body: string) {
  return body
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .join("\n");
}
