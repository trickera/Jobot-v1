package server

import "strings"

// The skill taxonomy exists to make an exported resume readable, and it very
// nearly cost a user their credibility instead. Two real failures, both found in
// the 2026-07-13 usability run, both shipped in a PDF the user was about to send:
//
//   - A backend resume listing Terraform and no CI/CD anything exported the line
//     "IaC & CI/CD: Terraform". The *label* asserted a competency the resume did
//     not have. Nobody added CI/CD to the resume — the category name did.
//
//   - A nurse's "Electronic Health Records (Epic)" exported as "Cloud:". The old
//     matcher was strings.Contains, and the Cloud alias "rds" is a substring of
//     "reco-rds-".
//
// Both are the same class of bug: the exported text said something the resume
// never said. Categories and labels ARE content — they are scanned by an ATS and
// read by a human, and the anti-invention Global Constraint applies to them
// exactly as it applies to a bullet. Hence the three rules this file enforces:
//
//  1. Labels are ATOMIC. "IaC & CI/CD" cannot exist, because one matched item
//     (Terraform) would vouch for two capabilities. A label names exactly one
//     thing, so an item that matched the category has, by construction, evidenced
//     the whole label. assertAtomicSkillLabels (and a test) keeps it that way.
//
//  2. Matching is token-aware, never raw substring. It goes through containsTerm
//     (scraper_go.go), whose word-boundary regex is what actually kills rds ⊂
//     records, iam ⊂ Miami, go ⊂ Django, and rds ⊂ standards.
//
//  3. A word that is only distinctive INSIDE its domain is exact-only: it must be
//     the entire skill, not a fragment of one. "Sous Chef" is not Infrastructure
//     as Code, "Flux Cored Arc Welding" is not CI/CD, and "Epic Games" is not a
//     hospital system. Word boundaries alone do not catch these — they are true
//     whole-word collisions — so the alias lists are split in two.
//
// The taxonomy is deliberately multi-domain. The old one had eight categories and
// all eight were DevOps, which is why a nurse's EHR had nowhere to land except a
// wrong bucket: with no clinical category in the table, the only question the code
// could ask was "which flavour of infrastructure is this?".

// resumeSkillCategory groups skill tokens under one ATS-friendly label.
//
// terms match anywhere inside a skill, on word boundaries ("terraform" matches
// "Terraform Cloud"). exact match only when they ARE the whole skill, and are
// reserved for words that are common outside this category.
type resumeSkillCategory struct {
	label string
	terms []string
	exact []string
}

// resumeSkillCategories is ordered: the first category whose alias matches wins.
// Domain-specific categories come before the general software ones so a clinical
// or financial token is never asked "but which kind of infrastructure are you?".
//
// It NEVER adds a skill the resume does not list, and never drops one — it only
// labels and orders tokens the user already has. Anything unmatched goes to
// "Additional" verbatim.
var resumeSkillCategories = []resumeSkillCategory{
	// ---- Healthcare / clinical ------------------------------------------------
	{
		label: "Clinical Systems",
		terms: []string{
			"electronic health record", "electronic medical record", "ehr", "emr",
			"cerner", "meditech", "allscripts", "athenahealth", "epic systems",
			"cpoe", "pyxis", "omnicell", "hl7", "icd-10", "cpt coding",
		},
		// "Epic" alone is a hospital system; "Epic Games" is not.
		exact: []string{"epic"},
	},
	{
		label: "Clinical Skills",
		terms: []string{
			"icu", "intensive care", "critical care", "telemetry", "stepdown",
			"step-down", "med surg", "med-surg", "medical surgical",
			"ventilator", "ventilator management", "mechanical ventilation",
			"crrt", "continuous renal replacement", "dialysis", "ecmo",
			"iv therapy", "intravenous", "phlebotomy", "central line",
			"tracheostomy", "wound care", "catheter", "foley",
			"triage", "code blue", "rapid response", "acls", "bls", "pals",
			"tncc", "ccrn", "nihss", "patient assessment", "patient safety",
			"medication administration", "charge nurse", "preceptor", "precepting",
			"infection control", "hipaa", "care coordination", "discharge planning",
		},
	},

	// ---- Finance --------------------------------------------------------------
	{
		label: "Finance",
		terms: []string{
			"financial modeling", "financial modelling", "fp&a", "gaap", "ifrs",
			"sarbanes-oxley", "valuation", "dcf", "budgeting", "forecasting",
			"variance analysis", "accounts payable", "accounts receivable",
			"reconciliation", "auditing", "quickbooks", "netsuite",
			"oracle financials", "bloomberg terminal", "cost accounting",
			"treasury", "risk management", "underwriting",
		},
		// SOX the compliance regime; SAP the ERP. Both are words elsewhere.
		exact: []string{"sox", "sap", "cpa", "cfa", "audit"},
	},

	// ---- Marketing ------------------------------------------------------------
	{
		label: "Marketing",
		terms: []string{
			"seo", "sem", "google ads", "meta ads", "facebook ads", "linkedin ads",
			"content marketing", "email marketing", "hubspot", "marketo",
			"mailchimp", "copywriting", "brand strategy", "social media",
			"conversion rate optimization", "demand generation", "lead generation",
			"go-to-market", "market research", "campaign management",
		},
		exact: []string{"ppc", "cro", "crm", "salesforce"},
	},

	// ---- Design ---------------------------------------------------------------
	{
		label: "Design",
		terms: []string{
			"figma", "sketch", "adobe xd", "photoshop", "illustrator", "indesign",
			"after effects", "user research", "wireframing", "wireframe",
			"prototyping", "design system", "usability testing", "typography",
			"interaction design", "visual design", "user experience",
			"user interface", "accessibility", "wcag",
		},
		exact: []string{"ux", "ui", "ux/ui", "ui/ux"},
	},

	// ---- Software: languages, data, platforms ---------------------------------
	{
		label: "Programming Languages",
		terms: []string{
			"python", "java", "javascript", "typescript", "golang", "c++", "c#",
			"ruby", "php", "rust", "kotlin", "swift", "scala", "perl", "elixir",
			"haskell", "objective-c", "sql", "bash", "shell scripting", "powershell",
		},
		// "Go" is a word. Django, Mongo and "go-to-market" are not the language.
		exact: []string{"go", "shell", "c"},
	},
	{
		label: "Frameworks",
		terms: []string{
			"react", "angular", "vue", "svelte", "next.js", "nuxt", "django",
			"flask", "fastapi", "spring boot", "ruby on rails", "express",
			".net", "asp.net", "laravel", "node.js", "nodejs", "gin", "echo framework",
		},
		exact: []string{"rails"},
	},
	{
		label: "Databases",
		terms: []string{
			"postgresql", "postgres", "mysql", "mariadb", "mongodb", "redis",
			"cassandra", "dynamodb", "oracle database", "sql server", "sqlite",
			"nosql", "snowflake", "bigquery", "redshift", "clickhouse", "cockroachdb",
		},
	},
	{
		label: "Cloud",
		terms: []string{
			"aws", "amazon web services", "azure", "gcp", "google cloud",
			"ec2", "eks", "aks", "gke", "ecs", "fargate", "cloudwatch",
			"cloudfront", "route53", "cloud",
			// Bounded: "rds" no longer reaches into "records"/"standards"/"awards",
			// and "iam" no longer reaches into "Miami". This is the UX-019 fix.
			"rds", "iam", "kms", "vpc", "s3",
		},
	},
	{
		label: "Infrastructure as Code",
		// Terraform is Infrastructure as Code. It is NOT CI/CD, and the label no
		// longer says it is. This is the UX-007 fix.
		terms: []string{
			"terraform", "opentofu", "cloudformation", "pulumi", "ansible",
			"puppet", "packer", "aws cdk", "bicep", "crossplane",
		},
		exact: []string{"chef", "salt"},
	},
	{
		label: "CI/CD",
		// Only an explicit CI/CD signal earns this label.
		terms: []string{
			"ci/cd", "cicd", "continuous integration", "continuous delivery",
			"continuous deployment", "github actions", "gitlab ci", "jenkins",
			"circleci", "travis ci", "teamcity", "bamboo", "tekton", "spinnaker",
			"argocd", "argo cd", "flux cd", "fluxcd", "azure devops", "build pipeline",
			"release pipeline",
		},
		exact: []string{"argo", "flux"},
	},
	{
		label: "Containers",
		terms: []string{
			"docker", "kubernetes", "k8s", "containerd", "podman", "openshift",
			"rancher", "helm", "kustomize", "docker compose", "container",
		},
	},
	{
		label: "Observability",
		terms: []string{
			"datadog", "grafana", "prometheus", "elasticsearch", "kibana",
			"logstash", "opentelemetry", "otel", "splunk", "loki", "jaeger",
			"new relic", "zabbix", "sentry", "nagios", "pagerduty",
		},
		exact: []string{"elk"},
	},
	{
		label: "Machine Learning",
		terms: []string{
			"tensorflow", "pytorch", "scikit-learn", "keras", "machine learning",
			"deep learning", "computer vision", "mlops", "hugging face",
		},
		exact: []string{"nlp", "llm"},
	},
	{
		label: "Data Engineering",
		terms: []string{
			"pandas", "numpy", "apache spark", "hadoop", "airflow", "dbt",
			"data warehouse", "data pipeline", "data modeling",
		},
		exact: []string{"etl", "spark"},
	},
	{
		label: "Analytics",
		terms: []string{
			"tableau", "power bi", "looker", "google analytics", "a/b testing",
			"data visualization", "statistical analysis",
		},
	},
	{
		label: "Security",
		terms: []string{
			"cybersecurity", "cyber security", "information security",
			"network security", "application security", "devsecops", "owasp",
			"siem", "penetration testing", "pentest", "threat modeling",
			"vulnerability management", "hashicorp vault", "iso 27001", "soc 2",
			"firewall", "rbac",
		},
		exact: []string{"waf", "vault"},
	},
	{
		label: "Operating Systems",
		terms: []string{
			"linux", "windows server", "unix", "rhel", "red hat", "ubuntu",
			"debian", "centos", "macos", "active directory",
		},
	},
	{
		label: "Networking",
		terms: []string{
			"tcp/ip", "dns", "bgp", "vpn", "load balancing", "networking",
			"content delivery network", "subnetting", "vlan",
		},
		exact: []string{"cdn"},
	},

	// ---- Cross-domain ---------------------------------------------------------
	{
		label: "Project Management",
		terms: []string{
			"jira", "confluence", "asana", "trello", "monday.com", "ms project",
			"stakeholder management", "roadmap", "backlog", "sprint planning",
			"resource planning",
		},
		exact: []string{"pmp"},
	},
	{
		label: "Methodologies",
		terms: []string{
			"agile", "scrum", "kanban", "waterfall", "lean", "six sigma",
			"site reliability", "gitops", "platform engineering", "devops",
			"incident management", "on-call",
		},
		exact: []string{"sre", "itil", "safe"},
	},
	{
		label: "Productivity",
		terms: []string{
			"microsoft excel", "powerpoint", "google workspace", "sharepoint",
			"outlook", "notion", "slack",
		},
		exact: []string{"excel", "word", "microsoft office"},
	},
}

// resumeSkillGroup is a labeled, ordered list of skills for rendering.
type resumeSkillGroup struct {
	label string
	items []string
}

// minGroupedSkills is the containment guard. One accidental hit — a chef, a
// welder, a "Cloud Kitchen" consultant — must not drag an entire non-technical
// resume into a technical taxonomy. Below this, and below half the pool, the
// exporter falls back to the flat Hard/Soft/Tools lines, which say nothing the
// resume did not already say.
const minGroupedSkills = 2

// categorizeSkill returns the index of the first category the skill belongs to,
// or -1. It is the only place a skill token is ever compared to an alias.
func categorizeSkill(skill string) int {
	n := strings.TrimSpace(strings.ToLower(normalizeText(skill)))
	if n == "" {
		return -1
	}
	// The whole-skill form, punctuation-stripped, for the exact-only aliases:
	// "Epic" -> "epic", "Go." -> "go", "(Chef)" -> "chef".
	whole := strings.Trim(n, " .,;:()[]{}/-")
	for i, cat := range resumeSkillCategories {
		for _, alias := range cat.exact {
			if whole == alias {
				return i
			}
		}
		for _, term := range cat.terms {
			// containsTerm (scraper_go.go) is word-boundary-aware for single
			// tokens and substring-only for multiword/"ci/cd"-style terms. This
			// single call is what stops "records" from being read as "rds".
			if containsTerm(n, term) {
				return i
			}
		}
	}
	return -1
}

// groupSkills categorizes hard skills and tools into resumeSkillCategories,
// keeping every token the resume already lists — no invention, no drops. Tokens
// matching no category fall into "Additional"; soft skills keep their own group.
//
// grouped is false when the taxonomy did not earn its keep (fewer than
// minGroupedSkills matches, or a minority of the pool). Callers then render the
// flat Hard/Soft/Tools lines instead. A flat list is never wrong; a confidently
// wrong label is.
func groupSkills(s ResumeSkills) (groups []resumeSkillGroup, grouped bool) {
	pool := make([]string, 0, len(s.Hard)+len(s.Tools))
	for _, sk := range append(append([]string{}, s.Hard...), s.Tools...) {
		if token := strings.TrimSpace(sk); token != "" {
			pool = append(pool, token)
		}
	}

	buckets := make([][]string, len(resumeSkillCategories))
	var additional []string
	matched := 0
	for _, token := range pool {
		if i := categorizeSkill(token); i >= 0 {
			buckets[i] = append(buckets[i], token)
			matched++
			continue
		}
		additional = append(additional, token)
	}

	if matched < minGroupedSkills || matched*2 < len(pool) {
		return nil, false
	}

	for i, cat := range resumeSkillCategories {
		if len(buckets[i]) > 0 {
			groups = append(groups, resumeSkillGroup{label: cat.label, items: buckets[i]})
		}
	}
	if len(additional) > 0 {
		groups = append(groups, resumeSkillGroup{label: "Additional", items: additional})
	}
	if len(s.Soft) > 0 {
		groups = append(groups, resumeSkillGroup{label: "Soft Skills", items: s.Soft})
	}

	// Fail safe, not fail silent: if the grouping ever loses, duplicates or
	// invents a token, drop the whole thing and emit the flat list. An export is
	// the last thing a user reads before sending the document to an employer.
	if !skillGroupsPreserveSource(groups, pool, s.Soft) {
		return nil, false
	}
	return groups, true
}

// skillGroupsPreserveSource is the anti-invention invariant for the grouped
// rendering: the multiset of emitted items must equal the multiset of source
// skills, exactly. Nothing added, nothing dropped, nothing duplicated.
func skillGroupsPreserveSource(groups []resumeSkillGroup, pool []string, soft []string) bool {
	want := map[string]int{}
	for _, item := range pool {
		want[item]++
	}
	for _, item := range soft {
		want[item]++
	}
	got := map[string]int{}
	for _, g := range groups {
		for _, item := range g.items {
			got[item]++
		}
	}
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

// labelIsAtomic reports whether a category label names exactly one capability.
//
// A compound label is banned outright: it lets one matched item vouch for a
// second capability the resume never evidenced, which is exactly how
// "IaC & CI/CD: Terraform" reached a PDF. Enforced by
// TestSkillCategoryLabelsAreAtomic over the whole taxonomy.
//
// "CI/CD" is the one legitimate slash — a single named practice, not two.
func labelIsAtomic(label string) bool {
	if label == "CI/CD" {
		return true
	}
	for _, conjunction := range []string{"&", "/", " and ", ",", "+"} {
		if strings.Contains(strings.ToLower(label), conjunction) {
			return false
		}
	}
	return true
}
