package server

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"
)

const maxResumeUploadBytes = 8 << 20

func (a *api) uploadResume(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var payload resumeUploadRequest
	if err := jsonDecodeLimited(r.Body, &payload, maxResumeUploadBytes*2); err != nil {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", "Invalid upload request body."))
		return
	}

	result, err := a.configStore.saveResume(payload)
	if err != nil {
		a.logger.Printf("save resume: %v", err)
		switch {
		case errors.Is(err, errUnsupportedFormat):
			writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "unsupported_format", "Unsupported resume format. Use PDF, DOCX or TXT."))
		case errors.Is(err, errFileTooLarge):
			writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "file_too_large", "The resume file exceeds 8 MB."))
		case errors.Is(err, errInvalidFile):
			writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_file", "Invalid or empty resume file. Try another file."))
		default:
			writeResumeError(w, resumeErrorFor(http.StatusInternalServerError, "internal_error", "Something went wrong on our side. Check the app logs."))
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func jsonDecodeLimited(reader io.Reader, target any, maxBytes int64) error {
	limited := io.LimitReader(reader, maxBytes)
	return json.NewDecoder(limited).Decode(target)
}

func (s *configStore) saveResume(payload resumeUploadRequest) (resumeUploadResponse, error) {
	name := sanitizeResumeName(payload.FileName)
	if name == "" {
		return resumeUploadResponse{}, fmt.Errorf("%w: invalid or empty file name", errInvalidFile)
	}

	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".pdf" && ext != ".docx" && ext != ".txt" {
		return resumeUploadResponse{}, fmt.Errorf("%w: use PDF, DOCX or TXT", errUnsupportedFormat)
	}

	content, err := base64.StdEncoding.DecodeString(payload.ContentBase64)
	if err != nil {
		return resumeUploadResponse{}, fmt.Errorf("%w: content is not valid base64", errInvalidFile)
	}
	if len(content) == 0 {
		return resumeUploadResponse{}, fmt.Errorf("%w: file is empty", errInvalidFile)
	}
	if len(content) > maxResumeUploadBytes {
		return resumeUploadResponse{}, fmt.Errorf("%w: file exceeds 8 MB", errFileTooLarge)
	}

	resumeDir := filepath.Join(filepath.Dir(s.path), "resumes")
	originalDir := filepath.Join(resumeDir, "original")
	parsedDir := filepath.Join(resumeDir, "parsed")
	if err := os.MkdirAll(originalDir, 0o700); err != nil {
		return resumeUploadResponse{}, err
	}
	if err := os.MkdirAll(parsedDir, 0o700); err != nil {
		return resumeUploadResponse{}, err
	}

	storedName := time.Now().UTC().Format("20060102-150405") + "-" + name
	storedPath := filepath.Join(originalDir, storedName)
	if err := os.WriteFile(storedPath, content, 0o600); err != nil {
		return resumeUploadResponse{}, err
	}

	rawText, err := extractResumeText(storedPath, ext, content)
	if err != nil {
		rawText = ""
	}
	markdown := resumeMarkdown(name, rawText)
	markdownPath := filepath.Join(parsedDir, strings.TrimSuffix(storedName, ext)+".md")
	if err := os.WriteFile(markdownPath, []byte(markdown), 0o600); err != nil {
		return resumeUploadResponse{}, err
	}

	cleanExtracted := truncate(cleanResumeExtractedText(rawText), 24000)
	llmText := truncate(markdown, 24000)
	keywords := extractResumeKeywords(coalesce(cleanExtracted, llmText))
	suggestion := detectResumeSearchProfile(cleanExtracted)
	detectedLevels := suggestion.Levels
	if detectedLevels == "" {
		detectedLevels = defaultConfig().Form.Levels
	}

	var warnings []string
	if scannedPDFSuspected(ext, cleanExtracted) {
		warnings = append(warnings, "scanned_pdf_suspected")
	}
	if suggestion.Role == "" {
		warnings = append(warnings, "search_role_not_detected")
	}

	config, err := s.load()
	if err != nil {
		config = defaultConfig()
	}
	config.Form.ResumeName = name
	config.Form.ResumePath = storedPath
	config.Form.ResumeMarkdownPath = markdownPath
	config.Form.ResumeText = llmText
	config.Form.Role = suggestion.Role
	config.Form.Roles = suggestion.Role
	config.Form.Seniority = suggestion.Seniority
	config.Form.Levels = detectedLevels
	config.Form.SearchProfiles = ""
	config.Form.Keywords = strings.Join(keywords, ", ")
	config.Form.KeywordsForRoles = suggestion.Role
	if err := s.save(config); err != nil {
		return resumeUploadResponse{}, err
	}

	return resumeUploadResponse{
		FileName:          name,
		StoredPath:        storedPath,
		MarkdownPath:      markdownPath,
		Markdown:          markdown,
		ExtractedText:     cleanExtracted,
		Keywords:          keywords,
		DetectedRole:      suggestion.Role,
		DetectedSeniority: suggestion.Seniority,
		DetectedLevels:    detectedLevels,
		Warnings:          warnings,
	}, nil
}

// cleanResumeExtractedText normalizes line endings and trims empty lines without
// flattening the document or erasing column gaps. Those boundaries are evidence
// used by name redaction, parsing and offline profile detection.
func cleanResumeExtractedText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	lines := strings.Split(value, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

const (
	scannedPDFMinChars = 200
	scannedPDFMinWords = 40
)

// scannedPDFSuspected flags a PDF whose extracted text is too thin to be a
// real text layer (D10/CH-09) - the classic signature of a scanned/image-only
// PDF that a text extractor could only pull a handful of stray characters
// from (page furniture, headers, OCR noise already baked into the file).
func scannedPDFSuspected(ext string, extractedText string) bool {
	if ext != ".pdf" {
		return false
	}
	text := strings.TrimSpace(extractedText)
	if len(text) < scannedPDFMinChars {
		return true
	}
	return len(strings.Fields(text)) < scannedPDFMinWords
}

// candidateScoringTerms is the skill list that drives heuristic scoring and
// missing-keyword hints. User-edited keywords take priority; when empty the
// resume body is parsed automatically so score stays tied to the CV.
func candidateScoringTerms(config appConfig) []string {
	if terms := splitCSV(config.Form.Keywords); len(terms) > 0 {
		return terms
	}
	if resume := strings.TrimSpace(config.Form.ResumeText); resume != "" {
		return extractResumeKeywords(resume)
	}
	return nil
}

func formatCandidateScoringTerms(config appConfig) string {
	terms := candidateScoringTerms(config)
	if len(terms) == 0 {
		return "(nenhuma — envie o curriculo ou preencha keywords)"
	}
	return strings.Join(terms, ", ")
}

func sanitizeResumeName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	replacer := regexp.MustCompile(`[^a-zA-Z0-9._ -]+`)
	name = replacer.ReplaceAllString(name, "-")
	name = strings.Trim(name, ". ")
	return name
}

func extractResumeText(path string, ext string, content []byte) (string, error) {
	switch ext {
	case ".txt":
		return string(content), nil
	case ".docx":
		return extractDOCXText(bytes.NewReader(content), int64(len(content)))
	case ".pdf":
		return extractPDFText(path)
	default:
		return "", errors.New("formato nao suportado")
	}
}

func extractDOCXText(reader io.ReaderAt, size int64) (string, error) {
	zr, err := zip.NewReader(reader, size)
	if err != nil {
		return "", err
	}
	for _, file := range zr.File {
		if file.Name != "word/document.xml" {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		decoder := xml.NewDecoder(rc)
		var builder strings.Builder
		var paragraph []string
		for {
			token, err := decoder.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return "", err
			}
			switch value := token.(type) {
			case xml.CharData:
				text := strings.TrimSpace(string(value))
				if text != "" {
					paragraph = append(paragraph, text)
				}
			case xml.EndElement:
				if value.Name.Local == "p" && len(paragraph) > 0 {
					builder.WriteString(strings.Join(paragraph, " "))
					builder.WriteByte('\n')
					paragraph = nil
				}
			}
		}
		if len(paragraph) > 0 {
			builder.WriteString(strings.Join(paragraph, " "))
		}
		return strings.TrimSpace(builder.String()), nil
	}
	return "", errors.New("document.xml not found in DOCX")
}

func extractPDFText(path string) (string, error) {
	file, reader, err := pdf.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	plain, err := reader.GetPlainText()
	if err != nil {
		return "", err
	}
	var buffer bytes.Buffer
	if _, err := io.Copy(&buffer, plain); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

func resumeMarkdown(fileName string, raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	lines := strings.Split(raw, "\n")

	var content []string
	for _, line := range lines {
		line = cleanText(line)
		if line == "" {
			continue
		}
		content = append(content, line)
	}

	var builder strings.Builder
	builder.WriteString("# Curriculo\n\n")
	builder.WriteString("Arquivo original: ")
	builder.WriteString(fileName)
	builder.WriteString("\n\n")

	if len(content) == 0 {
		builder.WriteString("> Nao foi possivel extrair texto suficiente deste arquivo. Se for um PDF escaneado, sera necessario OCR.\n")
		return builder.String()
	}

	keywords := extractResumeKeywords(strings.Join(content, " "))
	if len(keywords) > 0 {
		builder.WriteString("## Keywords detectadas\n\n")
		for _, keyword := range keywords {
			builder.WriteString("- ")
			builder.WriteString(keyword)
			builder.WriteByte('\n')
		}
		builder.WriteByte('\n')
	}

	builder.WriteString("## Texto extraido\n\n")
	for _, paragraph := range groupResumeParagraphs(content) {
		builder.WriteString(paragraph)
		builder.WriteString("\n\n")
	}
	return truncate(builder.String(), 32000)
}

func groupResumeParagraphs(lines []string) []string {
	var paragraphs []string
	var current []string
	for _, line := range lines {
		if looksLikeResumeHeading(line) {
			if len(current) > 0 {
				paragraphs = append(paragraphs, strings.Join(current, " "))
				current = nil
			}
			paragraphs = append(paragraphs, "### "+line)
			continue
		}
		current = append(current, line)
		if len(strings.Join(current, " ")) > 420 {
			paragraphs = append(paragraphs, strings.Join(current, " "))
			current = nil
		}
	}
	if len(current) > 0 {
		paragraphs = append(paragraphs, strings.Join(current, " "))
	}
	return paragraphs
}

func looksLikeResumeHeading(line string) bool {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 4 || len(trimmed) > 64 {
		return false
	}
	lower := strings.ToLower(trimmed)
	headings := []string{
		"experiencia", "experience", "skills", "competencias", "habilidades", "formacao",
		"educacao", "education", "certificacoes", "certifications", "projetos", "projects",
		"resumo", "summary", "objetivo", "objective",
	}
	for _, heading := range headings {
		if strings.Contains(lower, heading) {
			return true
		}
	}
	return strings.ToUpper(trimmed) == trimmed && strings.Contains(trimmed, " ")
}

func extractResumeKeywords(text string) []string {
	normalized := normalizeText(text)
	keywords := make([]string, 0, 32)
	seen := map[string]bool{}
	for _, skill := range knownResumeSkillTerms {
		if containsTerm(normalized, skill) {
			appendResumeKeyword(&keywords, seen, skill)
		}
		if len(keywords) >= 32 {
			return keywords
		}
	}

	// When the domain dictionary already yielded a solid set of real skills,
	// require frequency-derived tokens to repeat (count >= 2) so one-off noise
	// (names, headings, stray words) does not dilute the keyword list. Sparse
	// resumes keep the permissive single-occurrence fallback for recall.
	minCount := 1
	if len(keywords) >= 5 {
		minCount = 2
	}

	text = strings.ToLower(text)
	re := regexp.MustCompile(`[a-z0-9+#./-]{2,}`)
	counts := map[string]int{}
	for _, token := range re.FindAllString(text, -1) {
		token = strings.Trim(token, ".-/")
		if token == "" || resumeStopWords[token] || len(token) < 3 || isResumeNoiseToken(token) {
			continue
		}
		counts[token]++
	}
	type pair struct {
		term  string
		count int
	}
	pairs := make([]pair, 0, len(counts))
	for term, count := range counts {
		pairs = append(pairs, pair{term: term, count: count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count == pairs[j].count {
			return pairs[i].term < pairs[j].term
		}
		return pairs[i].count > pairs[j].count
	})
	for _, pair := range pairs {
		if pair.count < minCount {
			continue
		}
		appendResumeKeyword(&keywords, seen, pair.term)
		if len(keywords) >= 32 {
			break
		}
	}
	return keywords
}

func appendResumeKeyword(keywords *[]string, seen map[string]bool, value string) {
	value = strings.TrimSpace(value)
	key := normalizeText(value)
	if value == "" || seen[key] {
		return
	}
	seen[key] = true
	*keywords = append(*keywords, value)
}

func isResumeNoiseToken(token string) bool {
	if resumeStopWords[token] {
		return true
	}
	if regexp.MustCompile(`^\d+$`).MatchString(token) {
		return true
	}
	if strings.ContainsAny(token, "0123456789") && len(token) < 5 {
		return true
	}
	return false
}

// knownResumeSkillTerms is a multi-domain dictionary so keyword extraction is
// useful for ANY user, not only DevOps/infra profiles. Matching is
// accent-insensitive and word-boundary aware (see containsTerm), so short
// acronyms only match as standalone words. Keep entries domain-agnostic and
// avoid personal or company-specific terms.
var knownResumeSkillTerms = []string{
	// Cloud / DevOps / Infra
	"AWS", "Azure", "GCP", "Terraform", "Kubernetes", "EKS", "AKS", "Docker", "Linux",
	"Windows Server", "VMware", "CI/CD", "GitHub Actions", "GitLab CI", "Jenkins", "ArgoCD",
	"Helm", "Ansible", "Shell Script", "Bash", "PowerShell", "Grafana", "Prometheus", "ELK",
	"Zabbix", "Datadog", "New Relic", "CloudWatch", "Nginx", "Apache", "Redis", "RabbitMQ",
	"Kafka", "Observability", "DevOps", "SRE", "Infraestrutura", "Suporte N1", "Suporte N2",
	"Suporte N3", "Active Directory", "DNS", "DHCP", "GLPI", "ITIL", "ServiceNow",
	// Software / Data
	"Python", "Go", "Java", "C#", ".NET", "JavaScript", "TypeScript", "Node.js", "React",
	"Angular", "Vue", "Spring", "Django", "FastAPI", "PHP", "Laravel", "Ruby", "Rails",
	"SQL", "PostgreSQL", "MySQL", "SQL Server", "Oracle", "MongoDB", "APIs REST", "GraphQL",
	"Microserviços", "Git", "Power BI", "Tableau", "Looker", "ETL", "Databricks", "Spark",
	"Pandas", "Data Warehouse", "Analytics", "Machine Learning",
	// Cybersecurity
	"SIEM", "SOC", "IAM", "Firewall", "Pentest", "LGPD", "ISO 27001", "Incident Response",
	"Vulnerability", "Splunk", "Wazuh", "Cloud Security", "Blue Team", "Red Team",
	// Health / Nursing
	"Enfermagem", "Enfermeiro", "Enfermeira", "UTI", "CTI", "Prontuário", "Triagem", "SAE",
	"SAMU", "Emergência", "Urgência", "Farmacologia", "Curativos", "Sinais Vitais", "Paciente",
	"Hospitalar", "Ambulatorial", "COREN", "Vacinação", "Home Care", "Centro Cirúrgico",
	"Cuidados Intensivos",
	// Pharmacy / Pharma
	"Anvisa", "Dispensação", "Farmacovigilância", "Boas Práticas", "Medicamentos",
	"Manipulação", "Controle de Qualidade", "RDC", "Farmácia Clínica", "Farmacêutico",
	// Finance / Accounting
	"Contabilidade", "Contábil", "Contador", "Conciliação", "Fluxo de Caixa", "Contas a Pagar",
	"Contas a Receber", "SPED", "Fiscal", "Tributário", "Orçamento", "Fechamento Contábil",
	"DRE", "Auditoria", "Custos", "Faturamento", "Tesouraria", "FP&A", "Excel",
	// Education
	"Pedagogia", "BNCC", "EAD", "Moodle", "Planejamento de Aula", "Avaliação", "Alfabetização",
	"Docência", "Didática", "Coordenação Pedagógica", "Tutoria",
	// Business / Admin / cross-domain tools
	"SAP", "ERP", "CRM", "Salesforce", "Jira", "Scrum", "Kanban", "Gestão de Projetos",
	"Atendimento ao Cliente", "Departamento Pessoal", "eSocial", "Folha de Pagamento",
	"Recrutamento",
}

var resumeStopWords = map[string]bool{
	"and": true, "com": true, "das": true, "dos": true, "para": true, "por": true, "que": true,
	"the": true, "with": true, "from": true, "this": true, "that": true, "uma": true, "como": true,
	"em": true, "de": true, "da": true, "do": true, "no": true, "na": true, "ao": true, "as": true,
	"os": true, "ou": true, "of": true, "to": true, "in": true, "on": true, "at": true, "for": true,
	"aos": true, "nas": true, "nos": true, "pela": true, "pelo": true, "pelas": true, "pelos": true,
	"seu": true, "sua": true, "seus": true, "suas": true, "meu": true, "minha": true, "mais": true,
	"menos": true, "muito": true, "muita": true, "sobre": true, "entre": true, "apos": true,
	"antes": true, "desde": true, "onde": true, "quando": true, "porque": true, "brasil": true,
	"brazil": true, "remoto": true, "remote": true, "hibrido": true,
	"hybrid": true, "presencial": true, "empresa": true, "cargo": true, "curriculo": true,
	"experiencia": true, "formacao": true, "certificacoes": true, "projetos": true, "telefone": true,
	"email": true, "linkedin": true, "github": true, "2020": true, "2021": true, "2022": true,
	"2023": true, "2024": true, "2025": true, "2026": true,
	"gest": true, "ticas": true, "cnico": true, "ncia": true, "automa": true,
	"porto": true, "alegre": true, "investimentos": true, "financeiro": true,
	// Resume section headers / generic filler that is never a useful skill.
	"habilidades": true, "competencias": true, "objetivo": true, "resumo": true, "contato": true,
	"endereco": true, "idade": true, "estado": true, "civil": true, "nacionalidade": true,
	"disponibilidade": true, "pretensao": true, "salarial": true, "referencias": true,
	"atividades": true, "responsavel": true, "atuacao": true, "area": true, "profissional": true,
	"trabalho": true, "setor": true, "funcao": true, "periodo": true, "atual": true,
	"atualmente": true, "presente": true, "conhecimento": true, "conhecimentos": true,
	"ferramentas": true, "tecnologias": true, "principais": true, "principal": true,
	"diferenciais": true, "requisitos": true, "responsabilidades": true, "beneficios": true,
	"vaga": true, "candidato": true, "nome": true, "sobrenome": true, "graduacao": true,
	"graduado": true, "cursando": true, "completo": true, "incompleto": true, "nivel": true,
	"anos": true, "meses": true, "data": true, "nascimento": true, "atuacoes": true,
	// Common Brazilian given names / surnames — noise as scoring keywords.
	"joao": true, "maria": true, "jose": true, "ana": true, "silva": true, "santos": true,
	"oliveira": true, "souza": true, "sousa": true, "pereira": true, "carlos": true,
	"paulo": true, "pedro": true, "lucas": true, "rafael": true, "fernando": true,
	"bruno": true, "gabriel": true, "rodrigo": true, "marcos": true, "antonio": true,
	"francisco": true, "juliana": true, "fernanda": true, "camila": true, "mariana": true,
}
